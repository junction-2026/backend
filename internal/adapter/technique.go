package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// jobContentKey is the Inputs member that carries Job.input.content to technique_classifier (see Inputs).
const jobContentKey = "job_content"

// Result members (shared change technique-all-labels).
type (
	modelResult struct {
		Model     string           `json:"model"`
		Cutoff    float64          `json:"cutoff"`
		Scores    []techniqueScore `json:"scores"`
		LatencyMS *float64         `json:"latency_ms"`
	}
	techniqueScore struct {
		Label       string  `json:"label"`
		Display     string  `json:"display"`
		Score       float64 `json:"score"`
		Model       string  `json:"model"`
		Cutoff      float64 `json:"cutoff"`
		AboveCutoff bool    `json:"above_cutoff"`
		Known       bool    `json:"known"`
	}
	techniqueDetection struct {
		Name        string   `json:"name"`
		Display     string   `json:"display"`
		Explanation string   `json:"explanation"`
		Models      []string `json:"models"`
	}
)

// nciModel is one selected classification model with its effective cutoff.
type nciModel struct {
	name   string
	cfg    *registry.NCIModel
	cutoff float64
}

// techniqueClassifier scores the English translation for every technique label with nci-technique and/or
// nci-social (selected by word count, called concurrently, never merged). Then the span step (technique_spans.go)
// adds detected[].spans in Job.input.content.
func (x *Executor) techniqueClassifier(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := def.Execution
	text, lang, ok := translationOf(req.Inputs["text"])
	if !ok || strings.TrimSpace(text) == "" {
		return nil, adapterErr("the translation result has no translated_text")
	}
	words := len(strings.Fields(text))
	var sel []nciModel // nci-technique before nci-social
	if words >= e.TechniqueMinWordsN() || words > e.SocialMaxWordsN() {
		sel = append(sel, nciModel{"nci-technique", e.Technique, e.Technique.CutoffOr(registry.DefaultTechniqueCutoff)})
	}
	if words <= e.SocialMaxWordsN() {
		sel = append(sel, nciModel{"nci-social", e.Social, e.Social.CutoffOr(registry.DefaultSocialCutoff)})
	}
	for _, m := range sel {
		if m.cfg == nil || m.cfg.Endpoint == "" {
			return nil, adapterErr("no %s endpoint configured", m.name)
		}
	}
	body, err := json.Marshal(map[string]any{"text": []string{text}})
	if err != nil {
		return nil, adapterErr("classifier request is not serializable")
	}

	// All or nothing: the first failure cancels the other call and is the run's error.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]modelResult, len(sel))
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first error
	)
	for i, m := range sel {
		wg.Go(func() {
			r, err := x.classify(cctx, def, req, m, body)
			if err != nil {
				once.Do(func() { first = err; cancel() })
				return
			}
			results[i] = r
		})
	}
	wg.Wait()
	if first != nil {
		return nil, first
	}

	det := []techniqueDetection{}
	for _, n := range registry.TechniqueNames {
		var models []string
		for _, r := range results {
			for _, sc := range r.Scores {
				if sc.Label == n && sc.AboveCutoff {
					models = append(models, r.Model)
				}
			}
		}
		if models != nil {
			i := techniqueDisplay[n]
			det = append(det, techniqueDetection{n, i.Display, i.Explanation, models})
		}
	}
	content, _ := req.Inputs[jobContentKey].(string)
	sp, err := x.techniqueSpans(ctx, def, req, content, lang, det)
	if err != nil {
		return nil, err
	}
	expLang := explanationLanguage(lang)
	for i := range sp.detected {
		explain(&sp.detected[i], expLang)
	}
	out, err := json.Marshal(map[string]any{
		"words": words, "results": results, "detected": sp.detected,
		"span_method": sp.method, "span_status": sp.status, "spans_truncated": sp.truncated,
		"explanation_language": expLang, "glossary_version": techniqueGlossary.Version,
	})
	if err != nil {
		return nil, adapterErr("classifier result is not serializable")
	}
	return out, nil
}

// classify sends body to one model and builds its ModelResult: the 18 known labels in names order, then unfamiliar
// labels in the model's order.
func (x *Executor) classify(ctx context.Context, def *registry.Analyzer, req Request, m nciModel, body []byte) (modelResult, error) {
	call := *def
	call.Execution.Endpoint, call.Execution.Headers = m.cfg.Endpoint, m.cfg.Headers
	resp, err := x.send(ctx, &call, req, http.MethodPost, m.cfg.Endpoint, body)
	if err != nil {
		return modelResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return modelResult{}, adapterErr("%s service returned HTTP %d", m.name, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if ce := ContextError(ctx); ce != nil {
			return modelResult{}, ce
		}
		return modelResult{}, adapterErr("reading %s response failed", m.name)
	}
	lists, latency, msg := parseTechniqueResponse(b, 1)
	if msg != "" {
		return modelResult{}, &job.RunError{Code: job.CodeInvalidOutput,
			Message: techniqueRedactor(def.Execution)("invalid " + m.name + " response: " + msg)}
	}
	l := lists[0]
	r := modelResult{Model: m.name, Cutoff: m.cutoff, LatencyMS: latency, Scores: make([]techniqueScore, 0, len(l.scores))}
	add := func(label, display string, known bool) {
		sc := l.scores[label]
		r.Scores = append(r.Scores, techniqueScore{label, display, sc, m.name, m.cutoff, sc >= m.cutoff, known})
	}
	for _, n := range registry.TechniqueNames {
		add(n, techniqueDisplay[n].Display, true)
	}
	for _, n := range l.extra {
		add(n, n, false)
	}
	return r, nil
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

// labelScores is one list of a model answer: every label's score, and the unfamiliar labels in the model's order.
type labelScores struct {
	scores map[string]float64
	extra  []string
}

// parseTechniqueResponse checks {"result": [[{label, score}, ...] x want], "latency_ms"?}: every list holds each
// known label once, no duplicate or empty label and scores in [0, 1]; unfamiliar labels are kept. It returns what
// is wrong otherwise. The message never repeats response content.
func parseTechniqueResponse(b []byte, want int) (out []labelScores, latency *float64, msg string) {
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
	known := map[string]bool{}
	for _, n := range registry.TechniqueNames {
		known[n] = true
	}
	for _, l := range lists {
		ls := labelScores{scores: make(map[string]float64, len(l))}
		for _, e := range l {
			switch {
			case e.Label == nil || *e.Label == "":
				return nil, nil, "a label is missing or empty"
			case e.Score == nil || !inUnit(*e.Score):
				return nil, nil, "a score is not a number in [0, 1]"
			}
			if _, dup := ls.scores[*e.Label]; dup {
				return nil, nil, "a label is duplicated"
			}
			ls.scores[*e.Label] = *e.Score
			if !known[*e.Label] {
				ls.extra = append(ls.extra, *e.Label)
			}
		}
		if len(ls.scores)-len(ls.extra) != len(known) {
			return nil, nil, "a list misses one of the 18 known technique labels"
		}
		out = append(out, ls)
	}
	if lm := bytes.TrimSpace(o.LatencyMS); len(lm) > 0 && string(lm) != "null" {
		var f float64
		if json.Unmarshal(lm, &f) != nil || f < 0 {
			return nil, nil, "latency_ms must be a non-negative number"
		}
		latency = &f
	}
	return out, latency, ""
}

func inUnit(f float64) bool { return f >= 0 && f <= 1 }

// techniqueRedactor hides every configured header value of all models and the span LLM, its api_key (and a bearer
// token without its scheme).
func techniqueRedactor(e registry.Execution) func(string) string {
	var keys []string
	var hdrs []map[string]string
	for _, m := range []*registry.NCIModel{e.Technique, e.Social} {
		if m != nil {
			hdrs = append(hdrs, m.Headers)
		}
	}
	if s := e.Spans; s != nil {
		if s.SentenceModel != nil {
			hdrs = append(hdrs, s.SentenceModel.Headers)
		}
		keys = append(keys, secrets(&s.Exec)...)
	}
	for _, h := range hdrs {
		for _, v := range h {
			keys = append(keys, v)
			if f := strings.Fields(v); len(f) == 2 {
				keys = append(keys, f[1])
			}
		}
	}
	return redactor(keys...)
}
