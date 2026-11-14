package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// LLM technique labels, in the order of shared "LLM technique labels" (also the order of labels_checked and findings).
const (
	labelEvidence = "evidence_certainty_mismatch"
	labelHeadline = "misleading_headline"
	labelCherry   = "statistical_cherry_picking"
)

var llmTechniqueLabels = []string{labelEvidence, labelHeadline, labelCherry}

// jobTitleKey is the Inputs member with Job.input.source.title, set only for jobs created with input (not scrape).
const jobTitleKey = "job_title"

// LLMTechniquesSchema is the JSON schema of the llm_techniques reply.
var LLMTechniquesSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"findings": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"label":       map[string]any{"type": "string"},
				"field":       map[string]any{"type": "string"},
				"text":        map[string]any{"type": "string"},
				"confidence":  map[string]any{"type": "number"},
				"explanation": map[string]any{"type": "string"},
			},
			"required":             []string{"label", "field", "text", "confidence", "explanation"},
			"additionalProperties": false,
		}},
	},
	"required":             []string{"findings"},
	"additionalProperties": false,
}

type (
	llmTechniqueSpan struct {
		Field string `json:"field"`
		Text  string `json:"text"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	llmTechniqueFinding struct {
		Label       string           `json:"label"`
		Source      string           `json:"source"`
		Name        string           `json:"name"`
		Definition  string           `json:"definition"`
		Explanation string           `json:"explanation"`
		Confidence  float64          `json:"confidence"`
		Span        llmTechniqueSpan `json:"span"`
	}
	llmTechniqueResult struct {
		Source              string                `json:"source"`
		TitleChecked        bool                  `json:"title_checked"`
		LabelsChecked       []string              `json:"labels_checked"`
		ExplanationLanguage string                `json:"explanation_language"`
		GlossaryVersion     string                `json:"glossary_version"`
		Truncated           bool                  `json:"truncated"`
		Findings            []llmTechniqueFinding `json:"findings"`
	}
	// llmCandidate is one finding of the reply before acceptance.
	llmCandidate struct {
		Label, Field, Text, Explanation string
		Confidence                      float64
	}
)

// llmTechniques asks the LLM for the LLM technique labels in the job's original content and, for a distinct
// headline of a job created with input, its title; it keeps only findings whose text is an exact substring.
func (x *Executor) llmTechniques(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := &def.Execution
	content, _ := req.Inputs[jobContentKey].(string)
	title, _ := req.Inputs[jobTitleKey].(string)
	_, src, _ := translationOf(req.Inputs["translation"])
	text, truncated := content, false
	if r := []rune(content); len(r) > e.MaxChars() {
		text, truncated = cutAtSpace(r, e.MaxChars()), true
	}
	if strings.TrimSpace(text) == "" {
		return nil, adapterErr("job input has no content to analyze")
	}
	res := llmTechniqueResult{Source: "llm", ExplanationLanguage: explanationLanguage(src),
		GlossaryVersion: techniqueGlossary.Version, Findings: []llmTechniqueFinding{}}
	res.TitleChecked = strings.TrimSpace(title) != "" && strings.TrimSpace(title) != strings.TrimSpace(content)
	if !res.TitleChecked {
		title = "" // never sent
	}
	for _, l := range llmTechniqueLabels {
		if l != labelHeadline || res.TitleChecked {
			res.LabelsChecked = append(res.LabelsChecked, l)
		}
	}
	var cands []llmCandidate
	_, err := LLM(ctx, def, llmTechniquesMessage(e, text, title, res.LabelsChecked, res.ExplanationLanguage), func(raw json.RawMessage) (err error) {
		cands, err = parseLLMFindings(raw)
		return err
	})
	if err != nil {
		if re, ok := err.(*job.RunError); ok && re.Code == job.CodeInvalidOutput {
			re.Message = "invalid llm_techniques reply: " + re.Message
		}
		return nil, err
	}
	var cut bool
	res.Findings, cut = acceptFindings(e, cands, content, title, res.LabelsChecked, res.ExplanationLanguage)
	res.Truncated = truncated || cut
	out, err := json.Marshal(res)
	if err != nil {
		return nil, adapterErr("llm_techniques result is not serializable")
	}
	return out, nil
}

// parseLLMFindings reads {findings: [{label, field, text, confidence, explanation}]}; messages never quote the reply.
func parseLLMFindings(raw json.RawMessage) ([]llmCandidate, error) {
	var in struct {
		Findings *[]*struct {
			Label, Field, Text, Explanation *string
			Confidence                      *float64
		} `json:"findings"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Findings == nil {
		return nil, errors.New("reply is not {findings: [{label, field, text, confidence, explanation}]}")
	}
	out := make([]llmCandidate, 0, len(*in.Findings))
	for i, f := range *in.Findings {
		if f == nil || f.Label == nil || f.Field == nil || f.Text == nil || f.Explanation == nil || f.Confidence == nil {
			return nil, fmt.Errorf("findings[%d] needs string label, field, text and explanation and a numeric confidence", i)
		}
		out = append(out, llmCandidate{*f.Label, *f.Field, *f.Text, *f.Explanation, *f.Confidence})
	}
	return out, nil
}

// acceptFindings applies shared "LLM span acceptance", "LLM finding filtering" and "LLM finding order"; cut reports
// findings dropped by the per-label limit.
func acceptFindings(e *registry.Execution, cands []llmCandidate, content, title string, labels []string, lang string) (out []llmTechniqueFinding, cut bool) {
	red := llmTechniquesRedactor(e)
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Confidence > cands[j].Confidence })
	type key struct {
		label string
		span  llmTechniqueSpan
	}
	seen, count := map[key]bool{}, map[string]int{}
	out = []llmTechniqueFinding{}
	for _, c := range cands {
		field, s := "content", content
		if c.Label == labelHeadline {
			field, s = "title", title
		}
		k := strings.Index(s, c.Text)
		if !slices.Contains(labels, c.Label) || c.Field != field || !inUnit(c.Confidence) || c.Confidence < e.MinConfidenceN() ||
			strings.TrimSpace(c.Explanation) == "" || c.Text == "" || k < 0 {
			continue
		}
		start := utf16Len(s[:k])
		sp := llmTechniqueSpan{field, c.Text, start, start + utf16Len(c.Text)}
		if seen[key{c.Label, sp}] { // equal label, field, start and end: the higher confidence came first
			continue
		}
		seen[key{c.Label, sp}] = true
		if count[c.Label] >= e.MaxFindingsPerLabelN() {
			cut = true
			continue
		}
		count[c.Label]++
		g := techniqueGlossary.Techniques[c.Label][lang]
		out = append(out, llmTechniqueFinding{c.Label, "llm", g.Name, g.Definition, red(c.Explanation), c.Confidence, sp})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Label != b.Label {
			return slices.Index(llmTechniqueLabels, a.Label) < slices.Index(llmTechniqueLabels, b.Label)
		}
		if a.Span.Field != b.Span.Field {
			return a.Span.Field == "title"
		}
		return a.Span.Start < b.Span.Start || a.Span.Start == b.Span.Start && a.Span.End < b.Span.End
	})
	return out, cut
}

var closeTagRe = regexp.MustCompile(`(?i)</(content|title)>`)

// llmTechniquesMessage is the untrusted content inside <content> and the title (only when checked) inside <title>,
// their closing tags escaped, then the labels asked for, the explanation language and the schema unless json_schema
// mode carries it.
func llmTechniquesMessage(e *registry.Execution, content, title string, labels []string, lang string) string {
	esc := func(s string) string { return closeTagRe.ReplaceAllString(s, `<\/$1>`) }
	var b strings.Builder
	b.WriteString("<content>\n" + esc(content) + "\n</content>\n")
	if title != "" {
		b.WriteString("<title>\n" + esc(title) + "\n</title>\n")
	}
	b.WriteString("<labels>" + strings.Join(labels, ", ") + "</labels>\n<language>" + lang + "</language>")
	if e.ResponseFormat != registry.FormatJSONSchema {
		s, _ := json.Marshal(LLMTechniquesSchema)
		b.WriteString("\n<schema>\n" + string(s) + "\n</schema>")
	}
	return b.String()
}

// llmTechniquesRedactor hides the API key, every header value and a bearer token without its scheme.
func llmTechniquesRedactor(e *registry.Execution) func(string) string {
	var keys []string
	for _, v := range secrets(e) {
		keys = append(keys, v)
		if f := strings.Fields(v); len(f) == 2 {
			keys = append(keys, f[1])
		}
	}
	keys = slices.DeleteFunc(keys, func(k string) bool { return len(k) < 4 }) // shorter values are no real secret
	return redactor(keys...)
}
