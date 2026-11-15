package adapter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func TestJobSourceLanguageBinding(t *testing.T) {
	def := &registry.Analyzer{Trigger: registry.TriggerAfterAll, Input: registry.Input{Bindings: map[string]string{"language": "$job.source_language"}}}
	tr := func(status, result string) *job.Job {
		return &job.Job{Input: &job.Input{}, Analyzers: []job.AnalyzerRun{{ID: "translation", Status: status, Result: json.RawMessage(result)}}}
	}
	cases := []struct {
		name string
		j    *job.Job
		want any
	}{
		{"ru", tr(job.RunSucceeded, `{"source_language":"ru"}`), "ru"},
		{"normalized", tr(job.RunSucceeded, `{"source_language":" RU "}`), "ru"},
		{"en", tr(job.RunSucceeded, `{"source_language":"en"}`), "en"},
		{"failed", tr(job.RunFailed, `{"source_language":"ru"}`), nil},
		{"skipped", tr(job.RunSkipped, `null`), nil},
		{"null", tr(job.RunSucceeded, `{"source_language":null}`), nil},
		{"empty", tr(job.RunSucceeded, `{"source_language":""}`), nil},
		{"word", tr(job.RunSucceeded, `{"source_language":"russian"}`), nil},
		{"non-string", tr(job.RunSucceeded, `{"source_language":7}`), nil},
		{"no translation run", &job.Job{Input: &job.Input{}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := Inputs(def, c.j)
			v, present := in["language"]
			require.True(t, present, "bound member is always present")
			require.Equal(t, c.want, v)
		})
	}
}

func TestUserMessageSourceLanguage(t *testing.T) {
	def := &registry.Analyzer{Execution: registry.Execution{ResponseFormat: registry.FormatJSONSchema}}
	require.Equal(t, "<content>\nx\n</content>\n<source_language>ru (Russian)</source_language>\n<analyzers>\n</analyzers>", LLMUserMessage(def, "x", "ru", nil))
	require.Equal(t, "<content>\nx\n</content>\n<source_language>en (English)</source_language>\n<analyzers>\n</analyzers>", LLMUserMessage(def, "x", "", nil))
}
