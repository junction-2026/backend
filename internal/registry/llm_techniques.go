package registry

import (
	"fmt"
	"reflect"
	"strings"
)

// AdapterLLMTechniques asks an LLM for three weaknesses outside the SemEval labels (shared llm-technique-result).
const AdapterLLMTechniques = "llm_techniques"

// DefaultLLMTechniquesPrompt is where the image ships config/llm_techniques/prompt.md.
const DefaultLLMTechniquesPrompt = "/etc/orchestrator/llm_techniques/prompt.md"

// MinConfidenceN is min_confidence with its default; MaxFindingsPerLabelN is max_findings_per_label with its default.
func (e Execution) MinConfidenceN() float64 {
	if e.MinConfidence == nil {
		return 0.5
	}
	return *e.MinConfidence
}
func (e Execution) MaxFindingsPerLabelN() int { return intOr(e.MaxFindingsPerLabel, 5) }

// checkLLMTechniques validates the llm_techniques settings, applies their defaults and rejects its own keys on other adapters.
func (a *Analyzer) checkLLMTechniques() error {
	e := &a.Execution
	if e.Adapter != AdapterLLMTechniques {
		if e.MinConfidence != nil || e.MaxFindingsPerLabel != nil {
			return fmt.Errorf("execution.min_confidence and max_findings_per_label are only allowed for adapter %s", AdapterLLMTechniques)
		}
		return nil
	}
	only := *e
	only.Adapter, only.Timeout, only.BaseURL, only.APIKey, only.Model, only.PromptFile = "", 0, "", "", "", ""
	only.Temperature, only.MaxTokens, only.Headers = 0, 0, nil
	only.MinConfidence, only.MaxFindingsPerLabel, only.MaxInputChars = nil, nil, nil
	if !reflect.ValueOf(only).IsZero() { // every other execution key belongs to another adapter
		return fmt.Errorf("only execution.base_url, api_key, model, prompt_file, temperature, max_tokens, headers, min_confidence, max_findings_per_label, max_input_chars and timeout are allowed for adapter %s", e.Adapter)
	}
	switch {
	case a.Trigger != TriggerOnDeps:
		return fmt.Errorf("adapter %s requires trigger %s", e.Adapter, TriggerOnDeps)
	case len(a.Input.Bindings) != 1 || !strings.HasPrefix(a.Input.Bindings["translation"], "$analyzers."):
		return fmt.Errorf("input.bindings for adapter %s must be exactly translation: $analyzers.<id>", e.Adapter)
	case e.MinConfidence != nil && !inUnit(*e.MinConfidence):
		return fmt.Errorf("execution.min_confidence must be between 0 and 1")
	case e.MaxFindingsPerLabel != nil && *e.MaxFindingsPerLabel < 1:
		return fmt.Errorf("execution.max_findings_per_label must be a positive integer")
	case e.MaxInputChars != nil && *e.MaxInputChars < 1:
		return fmt.Errorf("execution.max_input_chars must be a positive integer")
	case e.MaxTokens < 0:
		return fmt.Errorf("execution.max_tokens must be a positive integer")
	}
	if e.PromptFile == "" {
		e.PromptFile = DefaultLLMTechniquesPrompt
	}
	if e.MaxTokens == 0 {
		e.MaxTokens = 1500
	}
	e.ResponseFormat, e.ExtraBody = FormatJSONSchema, map[string]any{}
	return nil
}
