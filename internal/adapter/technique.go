package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// jobContentKey is the Inputs member that carries Job.input.content to technique_classifier (see Inputs).
const jobContentKey = "job_content"

// Result members (shared change technique-classifier-result).
type (
	techniqueScore struct {
		Name    string  `json:"name"`
		Display string  `json:"display"`
		Score   float64 `json:"score"`
	}
	techniqueDetection struct {
		Name        string  `json:"name"`
		Display     string  `json:"display"`
		Explanation string  `json:"explanation"`
		Score       float64 `json:"score"`
	}
	sentenceDetection struct {
		Text       string               `json:"text"`
		Start      int                  `json:"start"`
		End        int                  `json:"end"`
		Techniques []techniqueDetection `json:"techniques"`
	}
)

// techniqueClassifier scores the English translation for the 18 propaganda techniques: one batch POST
// {"text": [full text, sentence 1..N]} to the short or the long model, chosen by word count. Then the span step
// (technique_spans.go) adds detected[].spans in Job.input.content.
func (x *Executor) techniqueClassifier(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := def.Execution
	text, lang, ok := translationOf(req.Inputs["text"])
	if !ok || strings.TrimSpace(text) == "" {
		return nil, adapterErr("the translation result has no translated_text")
	}
	words := len(strings.Fields(text))
	modelName, model := "long", e.Long
	if words <= e.ShortMaxWordsN() {
		modelName, model = "short", e.Short
	}
	if model == nil || model.Endpoint == "" {
		return nil, adapterErr("no %s model endpoint configured", modelName)
	}
	redact := techniqueRedactor(e)

	sents := splitSentences(text)
	truncated := len(sents) > e.MaxSentencesN()
	if truncated {
		sents = sents[:e.MaxSentencesN()]
	}
	texts, first := []string{text}, 1 // first: index in the response of sents[0]
	if len(sents) == 1 && sents[0].Text == strings.TrimSpace(text) {
		first = 0 // the one sentence is the document: its scores are the document's
	} else {
		for _, s := range sents {
			texts = append(texts, s.Text)
		}
	}
	body, err := json.Marshal(map[string]any{"text": texts})
	if err != nil {
		return nil, adapterErr("classifier request is not serializable")
	}
	call := *def
	call.Execution.Endpoint, call.Execution.Headers = model.Endpoint, model.Headers
	resp, err := x.send(ctx, &call, req, http.MethodPost, model.Endpoint, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, adapterErr("classifier service returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if ce := ContextError(ctx); ce != nil {
			return nil, ce
		}
		return nil, adapterErr("reading classifier response failed")
	}
	scores, latency, msg := parseTechniqueResponse(b, len(texts))
	if msg != "" {
		return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: redact("invalid classifier response: " + msg)}
	}

	thresholds := make(map[string]float64, len(registry.TechniqueNames))
	for _, n := range registry.TechniqueNames {
		thresholds[n] = model.ThresholdFor(n)
	}
	doc := scores[0]
	all := make([]techniqueScore, 0, len(doc))
	for _, n := range registry.TechniqueNames {
		all = append(all, techniqueScore{n, techniqueDisplay[n].Display, doc[n]})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })

	type hit struct {
		s    sentence
		dets []techniqueDetection
	}
	var hits []hit
	for i, s := range sents {
		if d := detections(scores[first+i], thresholds); len(d) > 0 {
			hits = append(hits, hit{s, d})
		}
	}
	// Offsets index Job.input.content when the text is English and every reported sentence is found there in order.
	content, _ := req.Inputs[jobContentKey].(string)
	offs, offsIn := make([][2]int, len(hits)), "translation"
	for i, h := range hits {
		offs[i] = [2]int{h.s.Start, h.s.End}
	}
	if lang == "en" {
		found, pos, u := make([][2]int, len(hits)), 0, 0
		for i, h := range hits {
			k := strings.Index(content[pos:], h.s.Text)
			if k < 0 {
				found = nil
				break
			}
			start := u + utf16Len(content[pos:pos+k])
			found[i] = [2]int{start, start + utf16Len(h.s.Text)}
			pos, u = pos+k+len(h.s.Text), found[i][1]
		}
		if found != nil {
			offs, offsIn = found, "original"
		}
	}
	sentences := make([]sentenceDetection, len(hits))
	for i, h := range hits {
		sentences[i] = sentenceDetection{h.s.Text, offs[i][0], offs[i][1], h.dets}
	}
	sp, err := x.techniqueSpans(ctx, def, req, content, lang, detections(doc, thresholds))
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(map[string]any{
		"model": modelName, "words": words, "threshold": model.ThresholdFor(""), "thresholds": thresholds,
		"techniques": all, "detected": sp.detected, "max_score": all[0].Score,
		"sentences": sentences, "offsets_in": offsIn, "sentences_truncated": truncated, "latency_ms": latency,
		"span_method": sp.method, "span_status": sp.status, "spans_truncated": sp.truncated,
	})
	if err != nil {
		return nil, adapterErr("classifier result is not serializable")
	}
	return out, nil
}

// translationOf reads translated_text and source_language from the bound translation result.
func translationOf(v any) (text, lang string, ok bool) {
	b, err := json.Marshal(v)
	var r struct {
		T any `json:"translated_text"`
		L any `json:"source_language"`
	}
	if err != nil || json.Unmarshal(b, &r) != nil {
		return "", "", false
	}
	text, ok = r.T.(string)
	lang, _ = r.L.(string)
	return text, lang, ok
}

// detections lists the techniques at or above their threshold, highest score first.
func detections(scores map[string]float64, thresholds map[string]float64) []techniqueDetection {
	out := []techniqueDetection{}
	for _, n := range registry.TechniqueNames {
		if s := scores[n]; s >= thresholds[n] {
			i := techniqueDisplay[n]
			out = append(out, techniqueDetection{n, i.Display, i.Explanation, s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// parseTechniqueResponse checks {"result": [[{label, score} x18] x want], "latency_ms"?} and returns one
// name->score map per text, or what is wrong. The message never repeats response content.
func parseTechniqueResponse(b []byte, want int) (scores []map[string]float64, latency *float64, msg string) {
	var o struct {
		Result    json.RawMessage `json:"result"`
		LatencyMS json.RawMessage `json:"latency_ms"`
	}
	if b = bytes.TrimSpace(b); len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &o) != nil {
		return nil, nil, "body is not a JSON object"
	}
	var lists [][]struct {
		Label *string  `json:"label"`
		Score *float64 `json:"score"`
	}
	if json.Unmarshal(o.Result, &lists) != nil || lists == nil {
		return nil, nil, "result must be an array of arrays of {label, score}"
	}
	if len(lists) != want {
		return nil, nil, "result has a different number of lists than texts sent"
	}
	valid := map[string]bool{}
	for _, n := range registry.TechniqueNames {
		valid[n] = true
	}
	for _, l := range lists {
		m := make(map[string]float64, len(valid))
		for _, e := range l {
			switch {
			case e.Label == nil || !valid[*e.Label]:
				return nil, nil, "a label is missing or not a technique name"
			case e.Score == nil || !inUnit(*e.Score):
				return nil, nil, "a score is not a number in [0, 1]"
			}
			if _, dup := m[*e.Label]; dup {
				return nil, nil, "a label is duplicated"
			}
			m[*e.Label] = *e.Score
		}
		if len(m) != len(valid) {
			return nil, nil, "a list does not hold exactly the 18 technique labels"
		}
		scores = append(scores, m)
	}
	if lm := bytes.TrimSpace(o.LatencyMS); len(lm) > 0 && string(lm) != "null" {
		var f float64
		if json.Unmarshal(lm, &f) != nil || f < 0 {
			return nil, nil, "latency_ms must be a non-negative number"
		}
		latency = &f
	}
	return scores, latency, ""
}

func inUnit(f float64) bool { return f >= 0 && f <= 1 }

// techniqueRedactor hides every configured header value of all models and the span LLM, its api_key (and a bearer
// token without its scheme).
func techniqueRedactor(e registry.Execution) func(string) string {
	var keys []string
	models := []*registry.TechniqueModel{e.Short, e.Long}
	if s := e.Spans; s != nil {
		models = append(models, s.SentenceModel)
		keys = append(keys, secrets(&s.Exec)...)
	}
	for _, m := range models {
		if m == nil {
			continue
		}
		for _, v := range m.Headers {
			keys = append(keys, v)
			if f := strings.Fields(v); len(f) == 2 {
				keys = append(keys, f[1])
			}
		}
	}
	return redactor(keys...)
}
