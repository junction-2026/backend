package engine

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Preview builders (analysis-job-events "Result preview"): pure functions from an analyzer's result JSON to the
// compact preview carried by analyzer.succeeded. They never call a model, a provider or the network; a member that
// is missing or has the wrong type yields null (or [] for a list); a builder that cannot run yields null.

const (
	maxTopicPreview     = 5
	maxOutletsPreview   = 6
	maxFactChecksTop    = 3
	maxTechniquePreview = 8
	maxSpansPreview     = 3
	maxLLMFindings      = 6
)

var llmLabels = map[string]bool{"evidence_certainty_mismatch": true, "misleading_headline": true, "statistical_cherry_picking": true}

var previewBuilders = map[string]func(map[string]any) any{
	"translation":          translationPreview,
	"propaganda_detection": propagandaPreview,
	"topic_extraction":     topicPreview,
	"techniques":           techniquesPreview,
	"llm_techniques":       llmTechniquesPreview,
	"news_search":          newsSearchPreview,
	"fact_check":           factCheckPreview,
	"source_info":          sourceInfoPreview,
}

// buildPreview returns the preview of analyzer id's result, or nil (no shape, bad result or builder panic).
func buildPreview(id string, result json.RawMessage) (out any) {
	build := previewBuilders[id]
	if build == nil || len(result) == 0 {
		return nil
	}
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	var res map[string]any
	dec := json.NewDecoder(bytes.NewReader(result))
	dec.UseNumber() // numbers are copied verbatim
	if dec.Decode(&res) != nil || res == nil {
		return nil
	}
	return build(res)
}

func str(v any) any {
	if s, ok := v.(string); ok {
		return s
	}
	return nil
}

func num(v any) any {
	if n, ok := v.(json.Number); ok {
		return n
	}
	return nil
}

func boolean(v any) any {
	if b, ok := v.(bool); ok {
		return b
	}
	return nil
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func list(v any) []any { l, _ := v.([]any); return l }

// listLen is the length of a list member, null when it is not a list.
func listLen(v any) any {
	if l, ok := v.([]any); ok {
		return len(l)
	}
	return nil
}

// strs returns the string elements of a list, at most n of the first n elements (n < 0: all).
func strs(v any, key string, n int) []string {
	out := []string{}
	for i, e := range list(v) {
		if n >= 0 && i >= n {
			break
		}
		if key != "" {
			e = obj(e)[key]
		}
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func copyKeys(m map[string]any, kinds map[string]func(any) any) map[string]any {
	out := make(map[string]any, len(kinds))
	for k, conv := range kinds {
		out[k] = conv(m[k])
	}
	return out
}

func translationPreview(r map[string]any) any {
	sl := str(r["source_language"])
	skipped, _ := r["skipped"].(string)
	return map[string]any{"source_language": sl, "translated": skipped == "" && sl != "en"}
}

func propagandaPreview(r map[string]any) any {
	return map[string]any{"label": str(r["label"]), "score": num(r["score"])}
}

func topicPreview(r map[string]any) any {
	return map[string]any{
		"claims": strs(r["claims"], "text", maxTopicPreview), "topics": strs(r["topics"], "title", maxTopicPreview),
		"claims_total": len(list(r["claims"])), "topics_total": len(list(r["topics"])),
	}
}

func newsSearchPreview(r map[string]any) any {
	outlets, seen := []string{}, map[string]bool{}
	for _, a := range list(r["articles"]) {
		if d, ok := obj(a)["domain"].(string); ok && !seen[d] && len(outlets) < maxOutletsPreview {
			seen[d] = true
			outlets = append(outlets, d)
		}
	}
	return map[string]any{"articles": listLen(r["articles"]), "outlets": outlets, "languages": strs(r["languages"], "", -1)}
}

func factCheckPreview(r map[string]any) any {
	top := []any{}
	for _, f := range list(r["fact_checks"]) {
		if m := obj(f); m != nil && len(top) < maxFactChecksTop {
			top = append(top, copyKeys(m, map[string]func(any) any{
				"publisher": str, "rating": str, "rating_normalized": str, "url": str}))
		}
	}
	return map[string]any{"fact_checks": listLen(r["fact_checks"]), "top": top}
}

func sourceInfoPreview(r map[string]any) any {
	var summary, page any
	if m := obj(r["summary"]); m != nil {
		summary = m
	}
	if m := obj(r["page_outlet"]); m != nil {
		page = m
	}
	return map[string]any{"summary": summary, "page_outlet": page}
}

// ranked is an entry with its sort score (ok false: no score).
type ranked struct {
	entry map[string]any
	score float64
	ok    bool
}

func topRanked(rs []ranked, n int) []any {
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].ok != rs[j].ok {
			return rs[i].ok
		}
		return rs[i].ok && rs[i].score > rs[j].score
	})
	out := []any{}
	for i := 0; i < len(rs) && i < n; i++ {
		out = append(out, rs[i].entry)
	}
	return out
}

func floatOf(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func techniquesPreview(r map[string]any) any {
	models := []string{}
	best := map[string]float64{} // label -> highest above-cutoff score of any model
	for _, m := range list(r["results"]) {
		if name, ok := obj(m)["model"].(string); ok {
			models = append(models, name)
		}
		for _, s := range list(obj(m)["scores"]) {
			sc := obj(s)
			label, _ := sc["label"].(string)
			if f, ok := floatOf(sc["score"]); ok && sc["above_cutoff"] == true {
				if cur, seen := best[label]; !seen || f > cur {
					best[label] = f
				}
			}
		}
	}
	var rs []ranked
	detected := list(r["detected"])
	for _, d := range detected {
		m := obj(d)
		if m == nil {
			continue
		}
		pl := obj(m["plain_language"])
		spans := []any{}
		for _, s := range list(m["spans"]) {
			if sm := obj(s); sm != nil && len(spans) < maxSpansPreview {
				spans = append(spans, copyKeys(sm, map[string]func(any) any{"start": num, "end": num, "text": str, "why": str}))
			}
		}
		label, _ := m["name"].(string)
		score, ok := best[label]
		var sv any
		if ok {
			sv = score
		}
		rs = append(rs, ranked{map[string]any{
			"label": str(m["name"]), "name": str(pl["name"]), "definition": str(pl["definition"]), "why": str(pl["why"]),
			"score": sv, "models": strs(m["models"], "", -1), "spans": spans}, score, ok})
	}
	return map[string]any{
		"models": models, "explanation_language": str(r["explanation_language"]), "glossary_version": str(r["glossary_version"]),
		"detected_total": len(detected), "detected": topRanked(rs, maxTechniquePreview),
	}
}

func llmTechniquesPreview(r map[string]any) any {
	var rs []ranked
	findings := list(r["findings"])
	for _, f := range findings {
		m := obj(f)
		if label, _ := m["label"].(string); m == nil || !llmLabels[label] {
			continue
		}
		var span any
		if sm := obj(m["span"]); sm != nil {
			span = copyKeys(sm, map[string]func(any) any{"field": str, "start": num, "end": num, "text": str})
		}
		score, ok := floatOf(m["confidence"])
		rs = append(rs, ranked{map[string]any{
			"label": str(m["label"]), "source": "llm", "name": str(m["name"]), "definition": str(m["definition"]),
			"explanation": str(m["explanation"]), "confidence": num(m["confidence"]), "span": span}, score, ok})
	}
	return map[string]any{
		"source": "llm", "title_checked": boolean(r["title_checked"]), "explanation_language": str(r["explanation_language"]),
		"glossary_version": str(r["glossary_version"]), "findings_total": len(findings), "findings": topRanked(rs, maxLLMFindings),
	}
}
