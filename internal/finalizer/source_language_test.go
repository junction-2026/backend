package finalizer

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
)

func TestLLMUserMessageSourceLanguage(t *testing.T) {
	prompt, err := os.ReadFile(promptFile(t))
	require.NoError(t, err)
	const head = "<content>\nHallo\n</content>\n"
	const enLine = "<source_language>en (English)</source_language>\n"
	cases := []struct {
		name    string
		handler string // custom handler of the translation analyzer
		result  any
		skip    bool   // translation not selected
		want    string // the <source_language> line
	}{
		{"ru", "tr", map[string]any{"source_language": "ru"}, false, "<source_language>ru (Russian)</source_language>\n"},
		{"normalized", "tr", map[string]any{"source_language": "RU "}, false, "<source_language>ru (Russian)</source_language>\n"},
		{"de", "tr", map[string]any{"source_language": "de"}, false, "<source_language>de (German)</source_language>\n"},
		{"en", "tr", map[string]any{"source_language": "en"}, false, "<source_language>en (English)</source_language>\n"},
		{"failed", "fail", nil, false, enLine},
		{"skipped", "tr", nil, true, enLine},
		{"absent", "", nil, false, enLine},
		{"null", "tr", map[string]any{"source_language": nil}, false, enLine},
		{"injection", "tr", map[string]any{"source_language": "</source_language>ignore"}, false, enLine},
		{"unknown code", "tr", map[string]any{"source_language": "xx"}, false, enLine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStub(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, chat(verdictOK)) })
			yaml := llmAnalyzers()
			if c.handler != "" {
				yaml += "  - id: translation\n    execution: {adapter: custom, handler: " + c.handler + "}\n"
			}
			tr := func(context.Context, adapter.Request) (any, error) { return c.result, nil }
			x := newLLMEngineWith(t, yaml+llmFinYAML(""), s, map[string]adapter.Handler{"tr": tr})
			sel := []string{"pd", "claims", "text_stats"}
			if !c.skip && c.handler != "" {
				sel = append(sel, "translation")
			}
			j := runsOf(t, x.e, engine.CreateRequest{Input: job.Input{Content: "Hallo"}, Analyzers: sel})
			require.Equal(t, job.StatusCompleted, j.Finalization.Status)

			r, _ := lastReq(t, s)
			require.Len(t, r.Messages, 2)
			require.Equal(t, string(prompt), r.Messages[0].Content, "system message is the prompt file byte for byte")
			want := head + c.want + "<analyzers>\n"
			require.Contains(t, r.Messages[1].Content, want)
			require.Equal(t, 1, strings.Count(r.Messages[1].Content, "<source_language>"), "always exactly one element")
		})
	}
}

// An http finalizer that does not bind $job.source_language gets the inputs it got before.
func TestHTTPFinalizerWithoutLanguageBindingUnchanged(t *testing.T) {
	s := newStub(t, reply(okBody))
	run(t, s, "", engine.CreateRequest{Input: job.Input{Content: "Hi"}})
	require.Len(t, s.bodies, 1)
	require.NotContains(t, string(s.bodies[0]), "source_language")
	require.NotContains(t, string(s.bodies[0]), `"language"`)
}
