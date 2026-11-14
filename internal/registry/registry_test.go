package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func opts(env map[string]string) Options {
	return Options{
		Getenv:        func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		HandlerExists: func(n string) bool { return n == "text_stats" },
	}
}

func write(t *testing.T, name, content string) string {
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

const custom = "    execution: {adapter: custom, handler: text_stats}\n"

func TestLoadDefaultsAndExpansion(t *testing.T) {
	p := write(t, "r.yaml", `analyzers:
  - id: sentiment
    name: Sentiment
    category: tone
    execution:
      adapter: http_async
      endpoint: ${BASE}/sentiment
      headers: {Authorization: "Bearer ${TOKEN}"}
  - id: off
    enabled: false
    execution: {adapter: http_sync, endpoint: "${UNSET_BUT_DISABLED}"}
  - id: final_synthesis
    trigger: after_all_analyzers_settled
    execution: {adapter: custom, handler: text_stats, timeout: 90s}
    input:
      bindings: {all: $job.analyzers, cov: $job.coverage, c: $input, s: $input.source, id: $job.id}
`)
	r, err := Load(p, opts(map[string]string{"BASE": "http://svc", "TOKEN": "s3cret"}))
	require.NoError(t, err)
	s := r.Get("sentiment")
	require.True(t, s.Enabled)
	require.Equal(t, TriggerOnDeps, s.Trigger)
	require.Equal(t, "POST", s.Execution.Method)
	require.Equal(t, 30*time.Second, s.Execution.Timeout)
	require.Equal(t, time.Second, s.Execution.PollInterval)
	require.Equal(t, "http://svc/sentiment", s.Execution.Endpoint)
	require.Equal(t, "Bearer s3cret", s.Execution.Headers["Authorization"])
	require.Equal(t, "off", r.Get("off").Name)
	require.False(t, r.Get("off").Enabled)
	require.Equal(t, "${UNSET_BUT_DISABLED}", r.Get("off").Execution.Endpoint, "disabled analyzers are not expanded")
	require.Equal(t, "final_synthesis", r.Finalizer().ID)
	require.Equal(t, 90*time.Second, r.Finalizer().Execution.Timeout)
	require.Equal(t, []string{"final_synthesis", "off", "sentiment"}, ids(r))
}

func ids(r *Registry) (out []string) {
	for _, a := range r.All() {
		out = append(out, a.ID)
	}
	return
}

func TestLoadDirectory(t *testing.T) {
	d := t.TempDir()
	for name, body := range map[string]string{
		"b.yml":     "analyzers:\n  - id: b\n" + custom + "    depends_on: [a]\n",
		"a.yaml":    "analyzers:\n  - id: a\n" + custom + "  - id: c\n" + custom,
		"notes.txt": "not yaml: [",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(d, name), []byte(body), 0o600))
	}
	r, err := Load(d, opts(nil))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, ids(r))

	// the same id in two files names the second file
	require.NoError(t, os.WriteFile(filepath.Join(d, "z.yaml"), []byte("analyzers:\n  - id: a\n"+custom), 0o600))
	_, err = Load(d, opts(nil))
	require.ErrorContains(t, err, "z.yaml")
	require.ErrorContains(t, err, `"a"`)
	require.ErrorContains(t, err, "duplicate")
}

func TestLoadRejects(t *testing.T) {
	fin := "  - id: fin\n    trigger: after_all_analyzers_settled\n" + custom
	cases := []struct{ name, yaml, want string }{
		{"bad id", "analyzers:\n  - id: Bad-ID\n" + custom, "id must match"},
		{"unknown depends_on", "analyzers:\n  - id: a\n" + custom + "    depends_on: [ghost]\n", `unknown analyzer "ghost"`},
		{"self cycle", "analyzers:\n  - id: a\n" + custom + "    depends_on: [a]\n", "cycle"},
		{"cycle", "analyzers:\n  - id: a\n" + custom + "    depends_on: [b]\n  - id: b\n" + custom + "    depends_on: [a]\n", "cycle"},
		{"two finalizers", "analyzers:\n" + fin + strings.Replace(fin, "id: fin", "id: fin2", 1), "more than one enabled"},
		{"finalizer depends_on", "analyzers:\n  - id: a\n" + custom + fin + "    depends_on: [a]\n", "must not use depends_on"},
		{"depends on finalizer", "analyzers:\n" + fin + "  - id: a\n" + custom + "    depends_on: [fin]\n", "after_all_analyzers_settled analyzer"},
		{"job.analyzers on regular", "analyzers:\n  - id: a\n" + custom + "    input: {bindings: {x: $job.analyzers}}\n", "only allowed"},
		{"job.coverage on regular", "analyzers:\n  - id: a\n" + custom + "    input: {bindings: {x: $job.coverage}}\n", "only allowed"},
		{"analyzers binding not in depends_on", "analyzers:\n  - id: a\n" + custom + "  - id: b\n" + custom + "    input: {bindings: {x: $analyzers.a}}\n", "not in depends_on"},
		{"unknown binding", "analyzers:\n  - id: a\n" + custom + "    input: {bindings: {x: $nope}}\n", "unknown binding"},
		{"http without endpoint", "analyzers:\n  - id: a\n    execution: {adapter: http_sync}\n", "endpoint is required"},
		{"unknown handler", "analyzers:\n  - id: a\n    execution: {adapter: custom, handler: nope}\n", `unknown custom handler "nope"`},
		{"unknown adapter", "analyzers:\n  - id: a\n    execution: {adapter: grpc}\n", "unknown execution.adapter"},
		{"unknown trigger", "analyzers:\n  - id: a\n    trigger: sometimes\n" + custom, "unknown trigger"},
		{"invalid schema", "analyzers:\n  - id: a\n" + custom + "    output: {schema: {type: 12}}\n", "output.schema"},
		{"unset env", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: \"${NOPE}/x\"}\n", "environment variable NOPE is not set"},
		{"unset env in header", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: http://x, headers: {A: \"${NOPE}\"}}\n", "NOPE"},
		{"propaganda without endpoint", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection}\n", "endpoint is required"},
		{"propaganda empty endpoint", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: \"\"}\n", "endpoint is required"},
		{"propaganda GET", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x, method: GET}\n", "method must be POST"},
		{"propaganda handler", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x, handler: text_stats}\n", "handler"},
		{"propaganda poll_interval", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x, poll_interval: 2s}\n", "poll_interval"},
		{"propaganda bindings", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {x: $input}}\n", "bindings are not allowed"},
		{"propaganda unset env", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: \"${PROPAGANDA_DETECTION_URL}\"}\n", "environment variable PROPAGANDA_DETECTION_URL is not set"},
		{"empty file", "", "no analyzers defined"},
		{"empty mapping", "{}\n", "no analyzers defined"},
		{"empty list", "analyzers: []\n", "no analyzers defined"},
		{"unknown field", "analyzers:\n  - id: a\n    colour: red\n" + custom, "colour"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := write(t, "reg.yaml", c.yaml)
			_, err := Load(p, opts(nil))
			require.Error(t, err)
			require.ErrorContains(t, err, c.want)
			if c.name != "unknown field" && c.name != "bad id" && !strings.HasPrefix(c.name, "empty") {
				require.ErrorContains(t, err, "reg.yaml")
				require.Regexp(t, `analyzer "(a|b|fin|fin2)"`, err.Error())
			}
		})
	}
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), opts(nil))
	require.Error(t, err)
	_, err = Load(t.TempDir(), opts(nil)) // directory without yaml files
	require.ErrorContains(t, err, "no analyzer files")
}

func TestExpandedValuesNeverInErrors(t *testing.T) {
	p := write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: \"${H}/${MISSING}\", headers: {K: \"${SECRET}\"}}\n")
	_, err := Load(p, opts(map[string]string{"H": "http://host", "SECRET": "topsecret"}))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "topsecret")
	require.NotContains(t, err.Error(), "http://host")
}

func TestClosureAndOutputValidation(t *testing.T) {
	p := write(t, "r.yaml", `analyzers:
  - id: a
    execution: {adapter: custom, handler: text_stats}
  - id: b
    depends_on: [a]
    execution: {adapter: custom, handler: text_stats}
    output:
      schema:
        type: object
        required: [score]
        properties:
          score: {type: number, minimum: 0}
          nested: {type: object, properties: {n: {type: integer}}}
  - id: c
    depends_on: [b]
    execution: {adapter: custom, handler: text_stats}
`)
	r, err := Load(p, opts(nil))
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"b": true, "a": true}, r.Closure([]string{"b"}))

	b := r.Get("b")
	require.NoError(t, b.ValidateOutput([]byte(`{"score": 3}`)))
	require.ErrorContains(t, b.ValidateOutput([]byte(`{"score": "x"}`)), "/score")
	require.ErrorContains(t, b.ValidateOutput([]byte(`{}`)), "score")
	require.ErrorContains(t, b.ValidateOutput([]byte(`{"score": 1, "nested": {"n": 1.5}}`)), "/nested/n")
	require.NoError(t, r.Get("a").ValidateOutput([]byte(`{"anything": true}`)))
}

func TestPropagandaDetectionLoads(t *testing.T) {
	p := write(t, "r.yaml", `analyzers:
  - id: a
    execution: {adapter: propaganda_detection, endpoint: "${U}", method: POST, headers: {Authorization: "Bearer ${T}"}}
  - id: off
    enabled: false
    execution: {adapter: propaganda_detection, endpoint: "${UNSET}"}
`)
	r, err := Load(p, opts(map[string]string{"U": "http://svc/predict", "T": "tok"}))
	require.NoError(t, err)
	e := r.Get("a").Execution
	require.Equal(t, "http://svc/predict", e.Endpoint)
	require.Equal(t, "POST", e.Method)
	require.Equal(t, 30*time.Second, e.Timeout)
	require.Equal(t, "Bearer tok", e.Headers["Authorization"])
	require.False(t, r.Get("off").Enabled)
}

func TestPropagandaDetectionDocsExample(t *testing.T) {
	r, err := Load("../../docs/examples/propaganda_detection.yaml", opts(map[string]string{"PROPAGANDA_DETECTION_URL": "http://svc/predict"}))
	require.NoError(t, err)
	a := r.Get("propaganda_detection")
	require.NotNil(t, a)
	require.True(t, a.Enabled)
	require.Equal(t, "Propaganda detection", a.Name)
	require.Equal(t, "propaganda", a.Category)
	require.Equal(t, AdapterPropagandaDetection, a.Execution.Adapter)
	require.Equal(t, "http://svc/predict", a.Execution.Endpoint)
	require.Equal(t, 30*time.Second, a.Execution.Timeout)
	require.Empty(t, a.Execution.Headers)

	_, err = Load("../../docs/examples/propaganda_detection.yaml", opts(nil))
	require.ErrorContains(t, err, "PROPAGANDA_DETECTION_URL")
}

const llmKey = "sk-registry-SECRET"

// llmReg is an enabled llm finalizer (fields overridable through env) plus one analyzer.
func llmReg(t *testing.T, finExtra, analyzerExtra string, env map[string]string) (*Registry, error) {
	t.Helper()
	e := map[string]string{"URL": "http://llm", "KEY": llmKey, "MODEL": "m", "V": "x",
		"PROMPT": write(t, "prompt.md", "You are a test prompt.\n")}
	for k, v := range env {
		e[k] = v
	}
	p := write(t, "r.yaml", "analyzers:\n  - id: a\n"+custom+analyzerExtra+
		"  - id: fin\n    trigger: after_all_analyzers_settled\n    execution:\n      adapter: llm\n"+
		"      base_url: ${URL}\n      api_key: ${KEY}\n      model: ${MODEL}\n      prompt_file: ${PROMPT}\n"+finExtra)
	return Load(p, opts(e))
}

func TestLLMDefaultsAndExpansion(t *testing.T) {
	r, err := llmReg(t, "      headers: {X-Title: \"${V}\"}\n      extra_body: {top_p: 0.5, provider: {order: [\"${V}\"]}}\n",
		"    llm: {include: true, description: Reads labels.}\n", nil)
	require.NoError(t, err)
	e := r.Finalizer().Execution
	require.Equal(t, "http://llm", e.BaseURL)
	require.Equal(t, llmKey, e.APIKey)
	require.Equal(t, "m", e.Model)
	require.Equal(t, "You are a test prompt.\n", e.Prompt)
	require.Equal(t, 0.0, e.Temperature)
	require.Equal(t, 600, e.MaxTokens)
	require.Equal(t, 30*time.Second, e.Timeout)
	require.Equal(t, FormatJSONSchema, e.ResponseFormat)
	require.Equal(t, "x", e.Headers["X-Title"])
	require.Equal(t, map[string]any{"top_p": 0.5, "provider": map[string]any{"order": []any{"x"}}}, e.ExtraBody)
	require.Equal(t, &LLMInput{Include: true, Description: "Reads labels."}, r.Get("a").LLM)

	// defaults of the optional members
	p := write(t, "r.yaml", "analyzers:\n  - id: fin\n    trigger: after_all_analyzers_settled\n    enabled: false\n    execution: {adapter: llm}\n")
	r, err = Load(p, opts(nil))
	require.NoError(t, err, "a disabled llm finalizer is not checked")
	e = r.Get("fin").Execution
	require.Equal(t, DefaultPromptFile, e.PromptFile)
	require.Equal(t, map[string]any{}, e.ExtraBody)
	require.Empty(t, e.Headers)
}

func TestLLMRejects(t *testing.T) {
	type env = map[string]string
	cases := []struct {
		name, fin, analyzer, want string
		env                       env
	}{
		{"empty base_url", "", "", "execution.base_url is empty", env{"URL": ""}},
		{"empty api_key", "", "", "execution.api_key is empty", env{"KEY": " "}},
		{"empty model", "", "", "execution.model is empty", env{"MODEL": ""}},
		{"unreadable prompt", "", "", "execution.prompt_file cannot be read", env{"PROMPT": "/nonexistent/prompt.md"}},
		{"unknown response_format", "      response_format: xml\n", "", "execution.response_format must be", nil},
		{"extra_body model", "      extra_body: {model: other}\n", "", "execution.extra_body.model is not allowed", nil},
		{"extra_body messages", "      extra_body: {messages: []}\n", "", "execution.extra_body.messages is not allowed", nil},
		{"include without description", "", "    llm: {include: true}\n", "llm.description is required", nil},
		{"include with blank description", "", "    llm: {include: true, description: \"  \"}\n", "llm.description is required", nil},
		{"llm block on finalizer", "    llm: {include: true, description: x}\n", "", "llm block is not allowed", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := llmReg(t, c.fin, c.analyzer, c.env)
			require.Error(t, err)
			require.ErrorContains(t, err, c.want)
			require.Regexp(t, `analyzer "(a|fin)"`, err.Error())
			require.NotContains(t, err.Error(), llmKey)
		})
	}
	p := write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: llm, base_url: http://x, api_key: k, model: m}\n")
	_, err := Load(p, opts(nil))
	require.ErrorContains(t, err, "execution.adapter llm is only allowed on after_all_analyzers_settled analyzers")
	require.ErrorContains(t, err, `analyzer "a"`)
}
