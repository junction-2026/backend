package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/junction-2026/backend/internal/registry"
)

// techniqueSpan is a range of Job.input.content; Start/End are half-open UTF-16 code-unit offsets.
type techniqueSpan struct {
	Technique string `json:"technique"`
	Text      string `json:"text"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
}

// detectedTechnique is a Detection of `detected` with its spans.
type detectedTechnique struct {
	techniqueDetection
	Spans []techniqueSpan `json:"spans"`
}

// TechniqueSpanSchema is the JSON schema of the span LLM's reply.
var TechniqueSpanSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"phrases": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"technique": map[string]any{"type": "string"},
				"text":      map[string]any{"type": "string"},
			},
			"required":             []string{"technique", "text"},
			"additionalProperties": false,
		}},
	},
	"required":             []string{"phrases"},
	"additionalProperties": false,
}

// spanResult is what the span step adds to the result.
type spanResult struct {
	method, status string
	truncated      bool
	detected       []detectedTechnique
}

// techniqueSpans runs the span step for the detected techniques under spans.timeout. It never fails the run itself:
// errors become span_status failed or timeout with empty spans. Only the analyzer's own ctx ending returns an error.
func (x *Executor) techniqueSpans(ctx context.Context, def *registry.Analyzer, req Request, content, lang string, det []techniqueDetection) (spanResult, error) {
	r := spanResult{method: "phrase", status: "ok"}
	if lang == "en" {
		r.method = "sentence"
	}
	var found map[string][]techniqueSpan
	if s := def.Execution.Spans; s != nil && len(det) > 0 {
		sctx, cancel := context.WithTimeout(ctx, s.Timeout)
		var err error
		if r.method == "sentence" {
			found, r.truncated, err = x.sentenceSpans(sctx, def, req, content, det)
		} else {
			found, r.truncated, err = phraseSpans(sctx, def, content, det)
		}
		spanDeadline := errors.Is(sctx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if ce := ContextError(ctx); ce != nil {
				return r, ce
			}
			r.status, found, r.truncated = "failed", nil, false
			if spanDeadline {
				r.status = "timeout"
			}
			slog.Warn("technique spans not computed", "job_id", req.JobID, "analyzer_id", req.AnalyzerID,
				"span_status", r.status, "error", techniqueRedactor(def.Execution)(err.Error()))
		}
	}
	for _, d := range det {
		sp := append([]techniqueSpan{}, found[d.Name]...)
		sort.SliceStable(sp, func(i, j int) bool {
			return sp[i].Start < sp[j].Start || sp[i].Start == sp[j].Start && sp[i].End < sp[j].End
		})
		uniq := sp[:0]
		for i, v := range sp {
			if i == 0 || v.Start != sp[i-1].Start || v.End != sp[i-1].End {
				uniq = append(uniq, v)
			}
		}
		r.detected = append(r.detected, detectedTechnique{d, uniq})
	}
	if r.detected == nil {
		r.detected = []detectedTechnique{}
	}
	return r, nil
}

// sentenceSpans scores the first max_sentences sentences of content in one POST {"text": [...]} to the sentence
// model; a sentence is a span of each detected technique it scores at or above the model's threshold for.
func (x *Executor) sentenceSpans(ctx context.Context, def *registry.Analyzer, req Request, content string, det []techniqueDetection) (map[string][]techniqueSpan, bool, error) {
	s := def.Execution.Spans
	m := s.SentenceModel
	sents := splitSentences(content)
	truncated := len(sents) > s.MaxSentencesN()
	if truncated {
		sents = sents[:s.MaxSentencesN()]
	}
	if len(sents) == 0 {
		return nil, truncated, nil
	}
	texts := make([]string, len(sents))
	for i, st := range sents {
		texts[i] = st.Text
	}
	body, err := json.Marshal(map[string]any{"text": texts})
	if err != nil {
		return nil, false, err
	}
	call := *def
	call.Execution.Endpoint, call.Execution.Headers = m.Endpoint, m.Headers
	resp, err := x.send(ctx, &call, req, http.MethodPost, m.Endpoint, body)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, false, fmt.Errorf("sentence model returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, false, errors.New("reading sentence model response failed")
	}
	scores, _, msg := parseTechniqueResponse(b, len(texts))
	if msg != "" {
		return nil, false, errors.New("invalid sentence model response: " + msg)
	}
	out := map[string][]techniqueSpan{}
	for i, st := range sents {
		for _, d := range det {
			if scores[i].scores[d.Name] >= m.ThresholdFor(d.Name) {
				out[d.Name] = append(out[d.Name], techniqueSpan{d.Name, st.Text, st.Start, st.End})
			}
		}
	}
	return out, truncated, nil
}

// phraseSpans asks the span LLM for phrases of content per detected technique and keeps only exact substrings
// of content (leftmost occurrence), at most max_phrases_per_technique each. Nothing is trimmed or repaired.
func phraseSpans(ctx context.Context, def *registry.Analyzer, content string, det []techniqueDetection) (map[string][]techniqueSpan, bool, error) {
	s := def.Execution.Spans
	text, truncated := content, false
	if r := []rune(content); len(r) > s.MaxInputCharsN() {
		text, truncated = cutAtSpace(r, s.MaxInputCharsN()), true
	}
	if strings.TrimSpace(text) == "" {
		return nil, false, nil
	}
	llm := &registry.Analyzer{ID: def.ID, Execution: s.Exec}
	raw, _, err := LLM(ctx, llm, spanUserMessage(&s.Exec, text, det))
	if err != nil {
		return nil, false, err
	}
	var in struct {
		Phrases *[]*struct{ Technique, Text *string } `json:"phrases"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Phrases == nil {
		return nil, false, errors.New("span llm reply is not {phrases: [{technique, text}]}")
	}
	detected := map[string]bool{}
	for _, d := range det {
		detected[d.Name] = true
	}
	out, seen := map[string][]techniqueSpan{}, map[[2]string]bool{}
	for _, p := range *in.Phrases {
		if p == nil || p.Technique == nil || p.Text == nil {
			return nil, false, errors.New("span llm reply has a phrase without string technique and text")
		}
		name, t := *p.Technique, *p.Text
		k := strings.Index(content, t)
		if !detected[name] || t == "" || k < 0 || seen[[2]string{name, t}] {
			continue
		}
		seen[[2]string{name, t}] = true
		if len(out[name]) >= s.MaxPhrasesN() {
			truncated = true
			continue
		}
		start := utf16Len(content[:k])
		out[name] = append(out[name], techniqueSpan{name, t, start, start + utf16Len(t)})
	}
	return out, truncated, nil
}

// spanUserMessage is the untrusted content inside <content> (its closing tag escaped, as in LLMUserMessage), then
// one JSON line per detected technique, then the schema unless json_schema mode carries it.
func spanUserMessage(e *registry.Execution, text string, det []techniqueDetection) string {
	var b strings.Builder
	b.WriteString("<content>\n" + closeContentRe.ReplaceAllString(text, `<\/$1>`) + "\n</content>\n<techniques>\n")
	for _, d := range det {
		j, _ := json.Marshal(map[string]string{"name": d.Name, "display": d.Display, "explanation": d.Explanation})
		b.Write(j)
		b.WriteByte('\n')
	}
	b.WriteString("</techniques>")
	if e.ResponseFormat != registry.FormatJSONSchema {
		s, _ := json.Marshal(TechniqueSpanSchema)
		b.WriteString("\n<schema>\n" + string(s) + "\n</schema>")
	}
	return b.String()
}
