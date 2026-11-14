package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ltYAML: translation (stand-in) plus an llm_techniques analyzer with the given execution tail.
func ltYAML(exec string) string {
	return "analyzers:\n  - id: translation\n" + custom + "  - id: llm_techniques\n    depends_on: [translation]\n" +
		"    input: {bindings: {translation: $analyzers.translation}}\n    execution:\n      adapter: llm_techniques\n" +
		"      base_url: ${B}\n      api_key: ${K}\n      model: ${M}\n" + exec
}

func ltEnv(t *testing.T) map[string]string {
	p, err := filepath.Abs("../../config/llm_techniques/prompt.md")
	require.NoError(t, err)
	return map[string]string{"B": "http://llm/v1", "K": "key", "M": "m", "P": p, "TOK": "tok"}
}

func TestLLMTechniquesDefaults(t *testing.T) {
	// the default prompt path exists only in the image
	_, err := Load(write(t, "r.yaml", ltYAML("")), opts(ltEnv(t)))
	require.ErrorContains(t, err, "execution.prompt_file cannot be read")
	require.ErrorContains(t, err, DefaultLLMTechniquesPrompt)

	r, err := Load(write(t, "r.yaml", ltYAML("      prompt_file: ${P}\n      headers: {Authorization: \"Bearer ${TOK}\"}\n")), opts(ltEnv(t)))
	require.NoError(t, err)
	e := r.Get("llm_techniques").Execution
	require.Equal(t, "http://llm/v1", e.BaseURL)
	require.Equal(t, "key", e.APIKey)
	require.Equal(t, "Bearer tok", e.Headers["Authorization"])
	require.Equal(t, 0.5, e.MinConfidenceN())
	require.Equal(t, 5, e.MaxFindingsPerLabelN())
	require.Equal(t, 20000, e.MaxChars())
	require.Equal(t, 1500, e.MaxTokens)
	require.Equal(t, FormatJSONSchema, e.ResponseFormat)
	require.Equal(t, 30*time.Second, e.Timeout)
	require.Contains(t, e.Prompt, "statistical_cherry_picking")

	r, err = Load(write(t, "r.yaml", ltYAML("      prompt_file: ${P}\n      min_confidence: 0.7\n      max_findings_per_label: 2\n"+
		"      max_input_chars: 500\n      max_tokens: 900\n      temperature: 0.2\n      timeout: 45s\n")), opts(ltEnv(t)))
	require.NoError(t, err)
	e = r.Get("llm_techniques").Execution
	require.Equal(t, 0.7, e.MinConfidenceN())
	require.Equal(t, 2, e.MaxFindingsPerLabelN())
	require.Equal(t, 500, e.MaxChars())
	require.Equal(t, 900, e.MaxTokens)
	require.Equal(t, 0.2, e.Temperature)
	require.Equal(t, 45*time.Second, e.Timeout)
}

func TestLLMTechniquesValidation(t *testing.T) {
	for tail, msg := range map[string]string{
		"      min_confidence: 1.5\n":          "execution.min_confidence must be between 0 and 1",
		"      min_confidence: -0.1\n":         "execution.min_confidence must be between 0 and 1",
		"      max_findings_per_label: 0\n":    "execution.max_findings_per_label must be a positive integer",
		"      max_input_chars: 0\n":           "execution.max_input_chars must be a positive integer",
		"      endpoint: http://x\n":           "only execution.base_url",
		"      response_format: json_object\n": "only execution.base_url",
		"      task: topic_extraction\n":       "only execution.base_url",
		"      spans: {timeout: 1s}\n":         "only allowed for adapter technique_classifier",
	} {
		_, err := Load(write(t, "r.yaml", ltYAML("      prompt_file: ${P}\n"+tail)), opts(ltEnv(t)))
		require.ErrorContains(t, err, msg, tail)
		require.ErrorContains(t, err, `"llm_techniques"`, tail)
	}
	_, err := Load(write(t, "r.yaml", strings.Replace(ltYAML("      prompt_file: ${P}\n"), "translation: $analyzers.translation", "text: $analyzers.translation", 1)), opts(ltEnv(t)))
	require.ErrorContains(t, err, "must be exactly translation: $analyzers.<id>")
	_, err = Load(write(t, "r.yaml", ltYAML("      prompt_file: ${P}\n")), opts(map[string]string{"B": "http://llm/v1", "K": "", "M": "m", "P": ltEnv(t)["P"]}))
	require.ErrorContains(t, err, "execution.api_key is empty; it is required for an enabled llm_techniques analyzer")
	_, err = Load(write(t, "r.yaml", ltYAML("      prompt_file: ${P}\n      min_confidence: soon\n")), opts(ltEnv(t)))
	require.Error(t, err)
}

func TestLLMTechniquesKeysOnlyForTheirAdapter(t *testing.T) {
	for _, k := range []string{"min_confidence: 0.5", "max_findings_per_label: 3"} {
		_, err := Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: http://x, "+k+"}\n"), opts(nil))
		require.ErrorContains(t, err, "only allowed for adapter llm_techniques", k)
		_, err = Load(write(t, "r.yaml", techYAML(techModels+"      "+k+"\n")), opts(techEnv))
		require.ErrorContains(t, err, "only allowed for adapter llm_techniques", k)
	}
}

func TestLLMTechniquesExampleLoads(t *testing.T) {
	dir := t.TempDir()
	b, err := os.ReadFile("../../docs/examples/llm_techniques.yaml")
	require.NoError(t, err)
	b = []byte(strings.ReplaceAll(string(b), DefaultLLMTechniquesPrompt, ltEnv(t)["P"]))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "llm_techniques.yaml"), b, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "translation.yaml"), []byte("analyzers:\n  - id: translation\n"+custom), 0o600))
	env := map[string]string{"LLM_TECHNIQUES_BASE_URL": "http://llm/v1", "LLM_TECHNIQUES_API_KEY": "k", "LLM_TECHNIQUES_MODEL": "m"}
	r, err := Load(dir, opts(env))
	require.NoError(t, err)
	a := r.Get("llm_techniques")
	require.True(t, a.Enabled)
	require.Equal(t, "propaganda", a.Category)
	require.Equal(t, []string{"translation"}, a.DependsOn)
	require.Equal(t, map[string]string{"translation": "$analyzers.translation"}, a.Input.Bindings)
	require.Equal(t, AdapterLLMTechniques, a.Execution.Adapter)
	require.True(t, a.LLM.Include)
	for _, w := range []string{"assessed by an LLM", "weaker evidence than the techniques model labels", "must not be treated as model detections"} {
		require.Contains(t, strings.Join(strings.Fields(a.LLM.Description), " "), w)
	}
	// the image ships the prompt at its default path
	df, err := os.ReadFile("../../Dockerfile")
	require.NoError(t, err)
	require.Contains(t, string(df), "COPY config/llm_techniques/prompt.md "+DefaultLLMTechniquesPrompt)
}
