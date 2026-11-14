package adapter

import (
	"encoding/json"
	"strings"

	"github.com/junction-2026/backend/internal/registry"
)

// The functions below expose the facts an adapter decides on before it does any work, so the engine's activity
// descriptions use the very same code as the run. They read only the resolved inputs: no network, LLM or
// translation call. ok is false when the fact cannot be determined.

// NewsSearchLanguages are the languages news_search will search, in search order.
func NewsSearchLanguages(def *registry.Analyzer, inputs map[string]any) (langs []string, ok bool) {
	langs = resolveLanguages(def.Execution.News.Languages, readSourceLanguage(inputs["translation"]))
	return langs, len(langs) > 0
}

// FactCheckClaims is the number of claims fact_check will search.
func FactCheckClaims(def *registry.Analyzer, inputs map[string]any) (n int, ok bool) {
	raw, _ := json.Marshal(inputs["extraction"])
	var ext map[string]json.RawMessage
	if json.Unmarshal(raw, &ext) != nil || ext == nil {
		return 0, false
	}
	e := &def.Execution
	return len(qualifyingClaims(ext["claims"], e.MinWorthiness(), e.FactCheckMaxClaims())), true
}

// TechniqueModels are the classification models techniques will run, nci-technique before nci-social.
func TechniqueModels(def *registry.Analyzer, inputs map[string]any) (models []string, ok bool) {
	text, _, ok := translationOf(inputs["text"])
	if !ok || strings.TrimSpace(text) == "" {
		return nil, false
	}
	for _, m := range selectModels(&def.Execution, len(strings.Fields(text))) {
		models = append(models, m.name)
	}
	return models, true
}

// LLMTitleChecked tells whether llm_techniques will check the headline.
func LLMTitleChecked(inputs map[string]any) bool {
	content, _ := inputs[jobContentKey].(string)
	title, _ := inputs[jobTitleKey].(string)
	return titleChecked(title, content)
}
