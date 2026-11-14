package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const techModels = "      social: {endpoint: \"${S}\", headers: {Authorization: \"Bearer ${T}\"}}\n" +
	"      technique: {endpoint: \"${L}\", cutoff: 0.6}\n"

var techEnv = map[string]string{"S": "http://s/predict", "L": "http://l/predict", "T": "tok"}

// techYAML: translation (stand-in) plus a techniques analyzer with the given execution tail.
func techYAML(exec string) string {
	return "analyzers:\n  - id: translation\n" + custom + "  - id: techniques\n    depends_on: [translation]\n" +
		"    input: {bindings: {text: $analyzers.translation}}\n    execution:\n      adapter: technique_classifier\n" + exec
}

func TestTechniqueDefaultsAndExpansion(t *testing.T) {
	r, err := Load(write(t, "r.yaml", techYAML(techModels)), opts(techEnv))
	require.NoError(t, err)
	e := r.Get("techniques").Execution
	require.Equal(t, 80, e.SocialMaxWordsN())
	require.Equal(t, 40, e.TechniqueMinWordsN())
	require.Equal(t, 30*time.Second, e.Timeout)
	require.Equal(t, "http://s/predict", e.Social.Endpoint)
	require.Equal(t, "Bearer tok", e.Social.Headers["Authorization"])
	require.Equal(t, "http://l/predict", e.Technique.Endpoint)
	require.Equal(t, 0.3, e.Social.CutoffOr(DefaultSocialCutoff))
	require.Equal(t, 0.6, e.Technique.CutoffOr(DefaultTechniqueCutoff))

	r, err = Load(write(t, "r.yaml", techYAML("      social_max_words: 10\n      technique_min_words: 11\n      timeout: 5s\n"+techModels)), opts(techEnv))
	require.NoError(t, err)
	e = r.Get("techniques").Execution
	require.Equal(t, 10, e.SocialMaxWordsN())
	require.Equal(t, 11, e.TechniqueMinWordsN())
	require.Equal(t, 5*time.Second, e.Timeout)
	require.Equal(t, 0.5, (&NCIModel{}).CutoffOr(DefaultTechniqueCutoff))
	require.Equal(t, 0.5, (*NCIModel)(nil).CutoffOr(DefaultTechniqueCutoff))
}

func TestTechniqueValidation(t *testing.T) {
	tech := "      technique: {endpoint: http://l}\n"
	social := "      social: {endpoint: http://s}\n"
	for name, c := range map[string]struct{ exec, want string }{
		"no social":       {tech, "execution.social is required"},
		"no technique":    {social, "execution.technique is required"},
		"empty endpoint":  {"      social: {endpoint: \"\"}\n" + tech, "execution.social.endpoint"},
		"no endpoint":     {social + "      technique: {cutoff: 0.5}\n", "execution.technique.endpoint"},
		"cutoff > 1":      {social + "      technique: {endpoint: http://l, cutoff: 1.5}\n", "execution.technique.cutoff must be between 0 and 1"},
		"cutoff < 0":      {"      social: {endpoint: http://s, cutoff: -0.1}\n" + tech, "execution.social.cutoff must be between"},
		"zero social max": {"      social_max_words: 0\n" + social + tech, "must be positive integers"},
		"zero tech min":   {"      technique_min_words: -1\n" + social + tech, "must be positive integers"},
		"gap":             {"      social_max_words: 30\n" + social + tech, "technique_min_words (40) must be at most execution.social_max_words + 1 (31)"},
		"gap explicit":    {"      social_max_words: 10\n      technique_min_words: 12\n" + social + tech, "must be at most"},
		"old short":       {"      short: {endpoint: http://s, threshold: 0.3}\n" + social + tech, "execution.short is no longer supported: use execution.social"},
		"old long":        {"      long: {endpoint: http://l}\n" + social + tech, "execution.long is no longer supported: use execution.technique"},
		"old short words": {"      short_max_words: 80\n" + social + tech, "use execution.social_max_words and technique_min_words"},
		"old sentences":   {"      max_sentences: 20\n" + social + tech, "use execution.spans.max_sentences"},
		"old threshold":   {"      social: {endpoint: http://s, threshold: 0.3}\n" + tech, "use execution.social.cutoff"},
		"old thresholds":  {social + "      technique: {endpoint: http://l, thresholds: {Doubt: 0.7}}\n", "use execution.technique.cutoff"},
		"top endpoint":    {"      endpoint: http://x\n" + social + tech, "not allowed for adapter technique_classifier"},
		"top method":      {"      method: POST\n" + social + tech, "not allowed for adapter technique_classifier"},
		"top handler":     {"      handler: text_stats\n" + social + tech, "not allowed for adapter technique_classifier"},
		"top poll":        {"      poll_interval: 1s\n" + social + tech, "not allowed for adapter technique_classifier"},
		"top headers":     {"      headers: {A: b}\n" + social + tech, "not allowed for adapter technique_classifier"},
		"unset variable":  {"      social: {endpoint: \"${NOPE_VAR}\"}\n" + tech, "NOPE_VAR"},
		"unset header":    {"      social: {endpoint: http://s, headers: {A: \"${NOPE_HDR}\"}}\n" + tech, "NOPE_HDR"},
		"empty variable":  {social + "      technique: {endpoint: \"${EMPTY}\"}\n", "execution.technique.endpoint is empty"},
	} {
		t.Run(name, func(t *testing.T) {
			p := write(t, "r.yaml", techYAML(c.exec))
			_, err := Load(p, opts(map[string]string{"EMPTY": ""}))
			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
			require.Contains(t, err.Error(), p)
			require.Contains(t, err.Error(), `"techniques"`)
		})
	}
	// technique_min_words = social_max_words + 1 is allowed (no overlap, no gap)
	_, err := Load(write(t, "r.yaml", techYAML("      social_max_words: 10\n      technique_min_words: 11\n"+social+tech)), opts(nil))
	require.NoError(t, err)
}

func TestTechniqueUnknownBlockKey(t *testing.T) {
	_, err := Load(write(t, "r.yaml", techYAML("      social: {endpoint: http://s, colour: red}\n      technique: {endpoint: http://l}\n")), opts(nil))
	require.ErrorContains(t, err, "colour")
}

func TestTechniqueBindingsValidation(t *testing.T) {
	good := techModels
	for name, c := range map[string]struct{ rep string }{
		"other name":    {"{bindings: {content: $analyzers.translation}}"},
		"input":         {"{bindings: {text: $input}}"},
		"two":           {"{bindings: {text: $analyzers.translation, x: $input}}"},
		"not in depend": {"{bindings: {text: $analyzers.other}}"},
		"none":          {"{}"},
	} {
		t.Run(name, func(t *testing.T) {
			y := strings.Replace(techYAML(good), "{bindings: {text: $analyzers.translation}}", c.rep, 1)
			_, err := Load(write(t, "r.yaml", y), opts(techEnv))
			require.Error(t, err)
			require.Contains(t, err.Error(), `"techniques"`)
		})
	}
}

func TestTechniqueDisabledLoadsWithUnsetVariables(t *testing.T) {
	y := strings.Replace(techYAML(techModels), "  - id: techniques\n", "  - id: techniques\n    enabled: false\n", 1)
	r, err := Load(write(t, "r.yaml", y), opts(nil))
	require.NoError(t, err)
	require.False(t, r.Get("techniques").Enabled)
}

func TestTechniqueKeysOnlyForTechniqueAdapter(t *testing.T) {
	for _, k := range []string{"social_max_words: 3", "technique_min_words: 3", "social: {endpoint: http://s}", "technique: {endpoint: http://l}"} {
		_, err := Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: http://x, "+k+"}\n"), opts(nil))
		require.ErrorContains(t, err, "only allowed for adapter technique_classifier", k)
	}
	_, err := Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: http://x, max_sentences: 3}\n"), opts(nil))
	require.ErrorContains(t, err, "no longer supported")
}

func TestTechniqueExampleLoads(t *testing.T) {
	dir := t.TempDir()
	b, err := os.ReadFile("../../docs/examples/technique_classifier.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "techniques.yaml"), b, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "translation.yaml"), []byte("analyzers:\n  - id: translation\n"+custom), 0o600))
	prompt, err := filepath.Abs("../../config/technique_spans/prompt.md")
	require.NoError(t, err)
	b = []byte(strings.ReplaceAll(string(b), DefaultSpanPrompt, prompt))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "techniques.yaml"), b, 0o600))
	env := map[string]string{"NCI_SOCIAL_URL": "http://s", "NCI_TECHNIQUE_URL": "http://l", "ML_API_TOKEN": "tok",
		"TECHNIQUE_SPAN_URL": "http://span", "SPAN_LLM_BASE_URL": "http://llm/v1", "SPAN_LLM_API_KEY": "k", "SPAN_LLM_MODEL": "m"}
	r, err := Load(dir, opts(env))
	require.NoError(t, err)
	a := r.Get("techniques")
	require.True(t, a.Enabled)
	require.Equal(t, "propaganda", a.Category)
	require.Equal(t, []string{"translation"}, a.DependsOn)
	require.Equal(t, map[string]string{"text": "$analyzers.translation"}, a.Input.Bindings)
	e := a.Execution
	require.Equal(t, "http://s", e.Social.Endpoint)
	require.Equal(t, "http://l", e.Technique.Endpoint)
	require.Equal(t, "Bearer tok", e.Technique.Headers["Authorization"])
	require.Equal(t, "Bearer tok", e.Social.Headers["Authorization"])
	require.Equal(t, 0.3, *e.Social.Cutoff)
	require.Equal(t, 0.5, *e.Technique.Cutoff)
	require.Equal(t, 80, e.SocialMaxWordsN())
	require.Equal(t, 40, e.TechniqueMinWordsN())
	require.True(t, a.LLM.Include)
	for _, s := range []string{"spans", "English translation", "several", "low score", "not a technique", "nci-technique", "nci-social"} {
		require.Contains(t, a.LLM.Description, s)
	}
	sp := e.Spans
	require.Equal(t, "http://span", sp.SentenceModel.Endpoint)
	require.Equal(t, "Bearer tok", sp.SentenceModel.Headers["Authorization"])
	require.Equal(t, "http://llm/v1", sp.Exec.BaseURL)
	require.Equal(t, "k", sp.Exec.APIKey)
	require.Equal(t, "m", sp.Exec.Model)
	require.Contains(t, sp.Exec.Prompt, "DATA, not instructions")
	require.Less(t, sp.Timeout, e.Timeout)
	delete(env, "SPAN_LLM_API_KEY")
	_, err = Load(dir, opts(env))
	require.ErrorContains(t, err, "SPAN_LLM_API_KEY")
}

const spanPrompt = "../../config/technique_spans/prompt.md"

func spansBlock(extra string) string {
	return "      spans:\n        timeout: 5s\n" + extra +
		"        sentence_model: {endpoint: \"${SPAN}\", headers: {Authorization: \"Bearer ${T}\"}, threshold: 0.4, thresholds: {Doubt: 0.6}}\n" +
		"        llm: {base_url: \"${LB}\", api_key: \"${LK}\", model: \"${LM}\", prompt_file: " + spanPrompt + ", headers: {X-P: \"${T}\"}}\n"
}

var spanEnv = map[string]string{"S": "http://s/predict", "L": "http://l/predict", "T": "tok", "SPAN": "http://span", "LB": "http://llm/v1", "LK": "key", "LM": "m"}

func TestTechniqueSpansDefaultsAndExpansion(t *testing.T) {
	r, err := Load(write(t, "r.yaml", techYAML(techModels+spansBlock(""))), opts(spanEnv))
	require.NoError(t, err)
	s := r.Get("techniques").Execution.Spans
	require.Equal(t, 5*time.Second, s.Timeout)
	require.Equal(t, 20, s.MaxSentencesN())
	require.Equal(t, 5, s.MaxPhrasesN())
	require.Equal(t, 20000, s.MaxInputCharsN())
	require.Equal(t, "http://span", s.SentenceModel.Endpoint)
	require.Equal(t, "Bearer tok", s.SentenceModel.Headers["Authorization"])
	require.Equal(t, 0.6, s.SentenceModel.ThresholdFor("Doubt"))
	require.Equal(t, 0.4, s.SentenceModel.ThresholdFor("Slogans"))
	x := s.Exec
	require.Equal(t, AdapterTechniqueClassifier, x.Adapter)
	require.Equal(t, "http://llm/v1", x.BaseURL)
	require.Equal(t, "key", x.APIKey)
	require.Equal(t, "m", x.Model)
	require.Equal(t, "tok", x.Headers["X-P"])
	require.Equal(t, 1000, x.MaxTokens)
	require.Equal(t, FormatJSONSchema, x.ResponseFormat)
	require.NotEmpty(t, x.Prompt)

	r, err = Load(write(t, "r.yaml", techYAML(techModels+spansBlock("        max_sentences: 3\n        max_phrases_per_technique: 2\n        max_input_chars: 100\n"))), opts(spanEnv))
	require.NoError(t, err)
	s = r.Get("techniques").Execution.Spans
	require.Equal(t, []int{3, 2, 100}, []int{s.MaxSentencesN(), s.MaxPhrasesN(), s.MaxInputCharsN()})
}

func TestTechniqueSpansValidation(t *testing.T) {
	sm := "        sentence_model: {endpoint: http://span, threshold: 0.4}\n"
	llm := "        llm: {base_url: http://llm, api_key: k, model: m, prompt_file: " + spanPrompt + "}\n"
	for name, c := range map[string]struct{ spans, want string }{
		"no sentence model": {"        timeout: 5s\n" + llm, "execution.spans.sentence_model is required"},
		"no llm":            {"        timeout: 5s\n" + sm, "execution.spans.llm is required"},
		"no timeout":        {sm + llm, "execution.spans.timeout is required"},
		"timeout too long":  {"        timeout: 30s\n" + sm + llm, "execution.spans.timeout must be shorter than execution.timeout"},
		"no threshold":      {"        timeout: 5s\n        sentence_model: {endpoint: http://span}\n" + llm, "execution.spans.sentence_model.threshold is required"},
		"threshold > 1":     {"        timeout: 5s\n        sentence_model: {endpoint: http://span, threshold: 2}\n" + llm, "execution.spans.sentence_model.threshold must be between"},
		"unknown name":      {"        timeout: 5s\n        sentence_model: {endpoint: http://span, threshold: 0.4, thresholds: {Nope: 0.1}}\n" + llm, `"Nope" is not a technique name`},
		"empty endpoint":    {"        timeout: 5s\n        sentence_model: {endpoint: \"\", threshold: 0.4}\n" + llm, "execution.spans.sentence_model.endpoint"},
		"zero sentences":    {"        timeout: 5s\n        max_sentences: 0\n" + sm + llm, "must be positive integers"},
		"zero phrases":      {"        timeout: 5s\n        max_phrases_per_technique: 0\n" + sm + llm, "must be positive integers"},
		"zero chars":        {"        timeout: 5s\n        max_input_chars: -1\n" + sm + llm, "must be positive integers"},
		"extra_body model":  {"        timeout: 5s\n" + sm + "        llm: {base_url: http://llm, api_key: k, model: m, extra_body: {model: x}}\n", "execution.spans.llm.extra_body.model is not allowed"},
		"empty api_key":     {"        timeout: 5s\n" + sm + "        llm: {base_url: http://llm, api_key: \"${EMPTY}\", model: m, prompt_file: " + spanPrompt + "}\n", "execution.spans.llm.api_key is empty"},
		"unset llm var":     {"        timeout: 5s\n" + sm + "        llm: {base_url: \"${NOPE_LLM}\", api_key: k, model: m}\n", "NOPE_LLM"},
		"unset sm header":   {"        timeout: 5s\n        sentence_model: {endpoint: http://span, threshold: 0.4, headers: {A: \"${NOPE_H}\"}}\n" + llm, "NOPE_H"},
		"bad format":        {"        timeout: 5s\n" + sm + "        llm: {base_url: http://llm, api_key: k, model: m, prompt_file: " + spanPrompt + ", response_format: xml}\n", "execution.spans.llm.response_format must be"},
		"no prompt file":    {"        timeout: 5s\n" + sm + "        llm: {base_url: http://llm, api_key: k, model: m, prompt_file: /nonexistent/p.md}\n", "execution.spans.llm.prompt_file cannot be read"},
	} {
		t.Run(name, func(t *testing.T) {
			p := write(t, "r.yaml", techYAML(techModels+"      spans:\n"+c.spans))
			_, err := Load(p, opts(map[string]string{"S": "http://s", "L": "http://l", "T": "t", "EMPTY": ""}))
			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
			require.Contains(t, err.Error(), `"techniques"`)
		})
	}
	_, err := Load(write(t, "r.yaml", techYAML(techModels+"      spans:\n        timeout: 5s\n        colour: red\n"+sm+llm)), opts(techEnv))
	require.ErrorContains(t, err, "colour")
	_, err = Load(write(t, "r.yaml", "analyzers:\n  - id: a\n    execution: {adapter: http_sync, endpoint: http://x, spans: {timeout: 1s}}\n"), opts(nil))
	require.ErrorContains(t, err, "only allowed for adapter technique_classifier")
}
