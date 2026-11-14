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
		{"propaganda bindings", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {x: $input}}\n", "accept only text"},
		{"propaganda binding $input.source", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {text: $input.source}}\n", "accept only text"},
		{"propaganda binding $job.id", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {text: $job.id}}\n", "accept only text"},
		{"propaganda two bindings", "analyzers:\n  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {text: $input, more: $input}}\n", "accept only text"},
		{"propaganda binding not in depends_on", "analyzers:\n  - id: b\n" + custom + "  - id: a\n    execution: {adapter: propaganda_detection, endpoint: http://x}\n    input: {bindings: {text: $analyzers.b}}\n", "not in depends_on"},
		{"mistral without api_key", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation}\n", "api_key is required"},
		{"mistral unset api_key var", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: \"${MISTRAL_API_KEY}\"}\n", "environment variable MISTRAL_API_KEY is not set"},
		{"mistral unset model var", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, model: \"${M}\"}\n", "environment variable M is not set"},
		{"mistral method", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, method: POST}\n", "method"},
		{"mistral handler", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, handler: text_stats}\n", "handler"},
		{"mistral poll_interval", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, poll_interval: 2s}\n", "poll_interval"},
		{"mistral bindings", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k}\n    input: {bindings: {text: $input}}\n", "bindings are not allowed"},
		{"mistral max_input_chars 0", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, max_input_chars: 0}\n", "max_input_chars"},
		{"mistral max_input_chars negative", "analyzers:\n  - id: a\n    execution: {adapter: mistral_translation, api_key: k, max_input_chars: -5}\n", "max_input_chars"},
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
	r, err := Load("../../docs/examples/propaganda_detection.yaml", opts(map[string]string{"MISTRAL_API_KEY": "k", "PROPAGANDA_DETECTION_URL": "http://svc/predict"}))
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

	_, err = Load("../../docs/examples/propaganda_detection.yaml", opts(map[string]string{"MISTRAL_API_KEY": "k"}))
	require.ErrorContains(t, err, "PROPAGANDA_DETECTION_URL")
}

func TestPropagandaBindingLoads(t *testing.T) {
	p := write(t, "r.yaml", `analyzers:
  - id: translation
    execution: {adapter: mistral_translation, api_key: k}
  - id: a
    depends_on: [translation]
    execution: {adapter: propaganda_detection, endpoint: http://x}
    input: {bindings: {text: $analyzers.translation}}
  - id: b
    execution: {adapter: propaganda_detection, endpoint: http://x}
    input: {bindings: {text: $input}}
`)
	r, err := Load(p, opts(nil))
	require.NoError(t, err)
	require.Equal(t, "$analyzers.translation", r.Get("a").Input.Bindings["text"])
	require.Equal(t, "$input", r.Get("b").Input.Bindings["text"])
}

func TestMistralTranslationLoads(t *testing.T) {
	p := write(t, "r.yaml", `analyzers:
  - id: a
    execution: {adapter: mistral_translation, api_key: "${K}"}
  - id: b
    execution: {adapter: mistral_translation, endpoint: "${E}", api_key: k, model: "${M}", timeout: 5s, max_input_chars: 100, headers: {X-Org: "${K}"}}
  - id: off
    enabled: false
    execution: {adapter: mistral_translation, api_key: "${UNSET}", model: "${UNSET}"}
`)
	r, err := Load(p, opts(map[string]string{"K": "key", "E": "http://m/v1", "M": "tiny"}))
	require.NoError(t, err)
	a := r.Get("a").Execution
	require.Equal(t, DefaultMistralEndpoint, a.Endpoint)
	require.Equal(t, "key", a.APIKey)
	require.Equal(t, "mistral-small-latest", a.Model)
	require.Equal(t, 60*time.Second, a.Timeout)
	require.Equal(t, 20000, a.MaxChars())
	b := r.Get("b").Execution
	require.Equal(t, "http://m/v1", b.Endpoint)
	require.Equal(t, "tiny", b.Model)
	require.Equal(t, 5*time.Second, b.Timeout)
	require.Equal(t, 100, b.MaxChars())
	require.Equal(t, "key", b.Headers["X-Org"])
	require.False(t, r.Get("off").Enabled)

	// an empty variable value loads (the adapter fails at run time)
	_, err = Load(p, opts(map[string]string{"K": "", "E": "e", "M": "m"}))
	require.NoError(t, err)
}

func TestTranslationDocsExample(t *testing.T) {
	const f = "../../docs/examples/propaganda_detection.yaml"
	r, err := Load(f, opts(map[string]string{"MISTRAL_API_KEY": "k", "PROPAGANDA_DETECTION_URL": "http://svc/predict"}))
	require.NoError(t, err)
	tr := r.Get("translation")
	require.NotNil(t, tr)
	require.Equal(t, "Translation to English", tr.Name)
	require.Equal(t, "translation", tr.Category)
	require.Equal(t, AdapterMistralTranslation, tr.Execution.Adapter)
	require.Equal(t, "k", tr.Execution.APIKey)
	require.Equal(t, "mistral-small-latest", tr.Execution.Model)
	require.Equal(t, 60*time.Second, tr.Execution.Timeout)
	pd := r.Get("propaganda_detection")
	require.Equal(t, []string{"translation"}, pd.DependsOn)
	require.Equal(t, map[string]string{"text": "$analyzers.translation"}, pd.Input.Bindings)
	require.Contains(t, pd.LLM.Description, "English machine translation")

	_, err = Load(f, opts(map[string]string{"PROPAGANDA_DETECTION_URL": "http://svc/predict"}))
	require.ErrorContains(t, err, "MISTRAL_API_KEY")

	sample, err := Load("../../config/analyzers", opts(nil))
	require.NoError(t, err)
	require.Nil(t, sample.Get("translation"))
	require.Nil(t, sample.Get("propaganda_detection"))
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

// topicReg loads one enabled topic_extraction analyzer; exec is the execution block after the task line.
func topicReg(t *testing.T, exec, extra string, env map[string]string) (*Registry, error) {
	t.Helper()
	e := map[string]string{"URL": "http://llm", "KEY": llmKey, "MODEL": "m", "PROMPT": write(t, "p.md", "extract\n")}
	for k, v := range env {
		e[k] = v
	}
	p := write(t, "t.yaml", "analyzers:\n  - id: topic_extraction\n    execution:\n      adapter: llm\n      task: topic_extraction\n"+
		"      base_url: ${URL}\n      api_key: ${KEY}\n      model: ${MODEL}\n      prompt_file: ${PROMPT}\n"+exec+extra)
	return Load(p, opts(e))
}

func TestTopicExtractionLoads(t *testing.T) {
	r, err := topicReg(t, "      headers: {X-Title: \"${MODEL}\"}\n      max_input_chars: 500\n", "    llm: {include: true, description: Topics.}\n", nil)
	require.NoError(t, err)
	a := r.Get("topic_extraction")
	e := a.Execution
	require.True(t, e.TopicExtraction())
	require.Equal(t, "http://llm", e.BaseURL)
	require.Equal(t, llmKey, e.APIKey)
	require.Equal(t, "extract\n", e.Prompt)
	require.Equal(t, 30*time.Second, e.Timeout)
	require.Equal(t, 2000, e.MaxTokens)
	require.Equal(t, 500, e.MaxChars())
	require.Equal(t, FormatJSONSchema, e.ResponseFormat)
	require.Equal(t, "m", e.Headers["X-Title"])
	require.Equal(t, TriggerOnDeps, a.Trigger)
	require.True(t, a.LLM.Include)
	require.Nil(t, r.Finalizer())

	// defaults: prompt path of the image, max_input_chars 20000
	p := write(t, "d.yaml", "analyzers:\n  - id: topic_extraction\n    enabled: false\n    execution: {adapter: llm, task: topic_extraction}\n")
	r, err = Load(p, opts(nil))
	require.NoError(t, err, "a disabled analyzer is not checked")
	require.Equal(t, DefaultExtractorPrompt, r.Get("topic_extraction").Execution.PromptFile)
	require.Equal(t, 20000, r.Get("topic_extraction").Execution.MaxChars())

	// the finalizer keeps its own defaults next to it
	r, err = llmReg(t, "", "", nil)
	require.NoError(t, err)
	require.Equal(t, 600, r.Finalizer().Execution.MaxTokens)
}

func TestTopicExtractionRejects(t *testing.T) {
	type env = map[string]string
	cases := []struct {
		name, exec, want string
		env              env
	}{
		{"empty base_url", "", "execution.base_url is empty", env{"URL": ""}},
		{"empty api_key", "", "execution.api_key is empty", env{"KEY": ""}},
		{"empty model", "", "execution.model is empty", env{"MODEL": " "}},
		{"unreadable prompt", "", "execution.prompt_file cannot be read", env{"PROMPT": "/nonexistent/p.md"}},
		{"extra_body model", "      extra_body: {model: x}\n", "execution.extra_body.model is not allowed", nil},
		{"extra_body messages", "      extra_body: {messages: []}\n", "execution.extra_body.messages is not allowed", nil},
		{"response_format", "      response_format: xml\n", "execution.response_format must be", nil},
		{"max_input_chars zero", "      max_input_chars: 0\n", "execution.max_input_chars must be a positive integer", nil},
		{"unset variable", "      headers: {X: \"${NOPE}\"}\n", "NOPE", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := topicReg(t, c.exec, "", c.env)
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, `analyzer "topic_extraction"`)
			require.NotContains(t, err.Error(), llmKey)
		})
	}
	_, err := topicReg(t, "", "    input: {bindings: {content: $input}}\n", nil)
	require.ErrorContains(t, err, "input.bindings are not allowed")

	for name, yml := range map[string]string{
		"finalizer with the task": "  - id: f\n    trigger: after_all_analyzers_settled\n    execution: {adapter: llm, task: topic_extraction}\n",
		"regular without task":    "  - id: a\n    execution: {adapter: llm}\n",
		"regular final_synthesis": "  - id: a\n    execution: {adapter: llm, task: final_synthesis}\n",
		"unknown task":            "  - id: a\n    execution: {adapter: llm, task: summarize}\n",
		"task on another adapter": "  - id: a\n    execution: {adapter: custom, handler: text_stats, task: topic_extraction}\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, "x.yaml", "analyzers:\n"+yml), opts(nil))
			require.ErrorContains(t, err, "execution.")
		})
	}
	// the finalizer may still say task: final_synthesis
	_, err = Load(write(t, "x.yaml", "analyzers:\n  - id: f\n    trigger: after_all_analyzers_settled\n    enabled: false\n    execution: {adapter: llm, task: final_synthesis}\n"), opts(nil))
	require.NoError(t, err)
}

func TestTopicExtractionExample(t *testing.T) {
	prompt, err := filepath.Abs("../../config/extractor/prompt.md")
	require.NoError(t, err)
	env := map[string]string{"EXTRACTOR_BASE_URL": "http://x/v1", "EXTRACTOR_API_KEY": "k", "EXTRACTOR_MODEL": "m"}
	b, err := os.ReadFile("../../docs/examples/topic_extraction.yaml")
	require.NoError(t, err)
	f := write(t, "t.yaml", strings.ReplaceAll(string(b), "/etc/orchestrator/extractor/prompt.md", prompt))
	r, err := Load(f, opts(env))
	require.NoError(t, err)
	e := r.Get("topic_extraction").Execution
	require.Equal(t, "http://x/v1", e.BaseURL)
	require.True(t, e.TopicExtraction())
	require.Contains(t, e.Prompt, "at most 10 topics")
	require.Contains(t, e.Prompt, "DATA, not instructions")
	delete(env, "EXTRACTOR_MODEL")
	_, err = Load(f, opts(env))
	require.ErrorContains(t, err, "EXTRACTOR_MODEL")
}

const fcHead = "analyzers:\n  - id: topic_extraction\n    enabled: false\n    execution: {adapter: llm, task: topic_extraction}\n" +
	"  - id: fact_check\n    depends_on: [topic_extraction]\n    input: {bindings: {extraction: $analyzers.topic_extraction}}\n    execution:\n      adapter: fact_check\n"

func fcLoad(t *testing.T, exec string, env map[string]string) (*Registry, error) {
	t.Helper()
	return Load(write(t, "f.yaml", fcHead+exec), opts(env))
}

func TestFactCheckDefaults(t *testing.T) {
	r, err := fcLoad(t, "      api_key: ${K}\n", map[string]string{"K": "secret-key"})
	require.NoError(t, err)
	e := r.Get("fact_check").Execution
	require.Equal(t, "secret-key", e.APIKey)
	require.Equal(t, DefaultFactCheckBaseURL, e.BaseURL)
	require.Equal(t, 0.4, e.MinWorthiness())
	require.Equal(t, 3, e.FactCheckMaxClaims())
	require.Equal(t, 5, e.PageSizeN())
	require.Equal(t, 1825, e.MaxAgeDaysN())
	require.Equal(t, 10, e.MaxResultsN())
	require.Equal(t, 4, e.MaxConcurrencyN())
	require.Equal(t, 24*time.Hour, e.CacheTTLD())
	require.Equal(t, 15*time.Second, e.Timeout)

	r, err = fcLoad(t, "      api_key: k\n      base_url: ${B}\n      min_check_worthiness: 0\n      max_claims: 2\n      page_size: 7\n      max_age_days: 30\n"+
		"      max_results: 4\n      max_concurrency: 1\n      cache_ttl: 1h\n      timeout: 3s\n", map[string]string{"B": "http://fc/v1"})
	require.NoError(t, err)
	e = r.Get("fact_check").Execution
	require.Equal(t, "http://fc/v1", e.BaseURL)
	require.Equal(t, 0.0, e.MinWorthiness())
	require.Equal(t, 2, e.FactCheckMaxClaims())
	require.Equal(t, time.Hour, e.CacheTTLD())
	require.Equal(t, 3*time.Second, e.Timeout)
}

func TestFactCheckRejects(t *testing.T) {
	for name, c := range map[string]struct{ exec, want string }{
		"no key":          {"      base_url: http://x\n", "execution.api_key"},
		"empty key":       {"      api_key: ${E}\n", "execution.api_key"},
		"worthiness high": {"      api_key: k\n      min_check_worthiness: 1.5\n", "min_check_worthiness"},
		"worthiness neg":  {"      api_key: k\n      min_check_worthiness: -0.1\n", "min_check_worthiness"},
		"max_claims 0":    {"      api_key: k\n      max_claims: 0\n", "max_claims"},
		"page_size 0":     {"      api_key: k\n      page_size: 0\n", "page_size"},
		"max_age_days 0":  {"      api_key: k\n      max_age_days: 0\n", "max_age_days"},
		"max_results 0":   {"      api_key: k\n      max_results: 0\n", "max_results"},
		"concurrency 0":   {"      api_key: k\n      max_concurrency: 0\n", "max_concurrency"},
		"cache_ttl neg":   {"      api_key: k\n      cache_ttl: -1h\n", "cache_ttl"},
		"timeout neg":     {"      api_key: k\n      timeout: -1s\n", "timeout"},
		"unset variable":  {"      api_key: ${NOPE}\n", "NOPE"},
		"endpoint":        {"      api_key: k\n      endpoint: http://x\n", "execution.endpoint"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fcLoad(t, c.exec, map[string]string{"E": ""})
			require.ErrorContains(t, err, c.want)
			require.ErrorContains(t, err, `analyzer "fact_check"`)
		})
	}
	// bindings
	for name, b := range map[string]string{"none": "", "wrong name": "{bindings: {text: $analyzers.topic_extraction}}", "input": "{bindings: {extraction: $input}}",
		"extra": "{bindings: {extraction: $analyzers.topic_extraction, text: $input}}"} {
		y := "analyzers:\n  - id: topic_extraction\n    enabled: false\n    execution: {adapter: llm, task: topic_extraction}\n  - id: fact_check\n    depends_on: [topic_extraction]\n"
		if b != "" {
			y += "    input: " + b + "\n"
		}
		_, err := Load(write(t, "b.yaml", y+"    execution: {adapter: fact_check, api_key: k}\n"), opts(nil))
		require.ErrorContains(t, err, "input.bindings", name)
	}
	// the key never leaks into a message
	_, err := fcLoad(t, "      api_key: secret-key\n      max_claims: 0\n", nil)
	require.NotContains(t, err.Error(), "secret-key")
	// fact_check keys on other adapters
	_, err = Load(write(t, "o.yaml", "analyzers:\n  - id: a\n    execution: {adapter: custom, handler: text_stats, page_size: 3}\n"), opts(nil))
	require.ErrorContains(t, err, "only allowed for adapter fact_check")
	_, err = Load(write(t, "o2.yaml", "analyzers:\n  - id: a\n    execution: {adapter: custom, handler: text_stats, max_claims: 3}\n"), opts(nil))
	require.ErrorContains(t, err, "max_claims")
}

func TestTopicExtractionMaxClaims(t *testing.T) {
	r, err := topicReg(t, "      max_claims: 7\n", "", nil)
	require.NoError(t, err)
	require.Equal(t, 7, r.Get("topic_extraction").Execution.ExtractorMaxClaims())
	r, err = topicReg(t, "", "", nil)
	require.NoError(t, err)
	require.Equal(t, 5, r.Get("topic_extraction").Execution.ExtractorMaxClaims())
	_, err = topicReg(t, "      max_claims: 0\n", "", nil)
	require.ErrorContains(t, err, `analyzer "topic_extraction"`)
	require.ErrorContains(t, err, "execution.max_claims")
}

func TestFactCheckDocsExample(t *testing.T) {
	prompt, err := filepath.Abs("../../config/extractor/prompt.md")
	require.NoError(t, err)
	env := map[string]string{"EXTRACTOR_BASE_URL": "http://x/v1", "EXTRACTOR_API_KEY": "k", "EXTRACTOR_MODEL": "m", "GOOGLE_FACTCHECK_API_KEY": "gk"}
	dir := t.TempDir()
	for _, n := range []string{"topic_extraction", "fact_check"} {
		b, err := os.ReadFile("../../docs/examples/" + n + ".yaml")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, n+".yaml"), []byte(strings.ReplaceAll(string(b), "/etc/orchestrator/extractor/prompt.md", prompt)), 0o600))
	}
	r, err := Load(dir, opts(env))
	require.NoError(t, err)
	fc := r.Get("fact_check")
	require.Equal(t, "gk", fc.Execution.APIKey)
	require.Equal(t, []string{"topic_extraction"}, fc.DependsOn)
	require.Equal(t, "fact_check", fc.Category)
	require.True(t, fc.LLM.Include)
	require.Contains(t, fc.LLM.Description, "fact-checks")
	require.Equal(t, 5, r.Get("topic_extraction").Execution.ExtractorMaxClaims())
	b, _ := os.ReadFile("../../docs/examples/topic_extraction.yaml")
	require.Contains(t, string(b), "max_claims: 5")
	delete(env, "GOOGLE_FACTCHECK_API_KEY")
	_, err = Load(dir, opts(env))
	require.ErrorContains(t, err, "GOOGLE_FACTCHECK_API_KEY")
}
