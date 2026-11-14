package registry

import (
	"fmt"
	"strings"
	"time"
)

// AdapterTechniqueClassifier is the propaganda technique classifier (short or long ML model chosen by text length).
const AdapterTechniqueClassifier = "technique_classifier"

// TechniqueNames are the 18 SemEval-2020 Task 11 technique names, in a fixed order (the order of ties in results).
var TechniqueNames = []string{
	"Loaded_Language", "Appeal_to_fear-prejudice", "Exaggeration,Minimisation", "Repetition", "Flag-Waving",
	"Name_Calling,Labeling", "Reductio_ad_hitlerum", "Black-and-White_Fallacy", "Causal_Oversimplification",
	"Whataboutism,Straw_Men,Red_Herring", "Straw_Man", "Red_Herring", "Doubt", "Appeal_to_Authority",
	"Thought-terminating_Cliches", "Bandwagon", "Slogans", "Obfuscation,Intentional_Vagueness,Confusion",
}

// TechniqueModel is one ML service of the technique_classifier adapter (the `short` or `long` block).
type TechniqueModel struct {
	Endpoint   string             `yaml:"endpoint"`
	Headers    map[string]string  `yaml:"headers"`
	Threshold  *float64           `yaml:"threshold"`
	Thresholds map[string]float64 `yaml:"thresholds"`
}

// ThresholdFor is the effective threshold of one technique (validated at load; tests may build models directly).
func (m TechniqueModel) ThresholdFor(name string) float64 {
	if t, ok := m.Thresholds[name]; ok {
		return t
	}
	if m.Threshold == nil {
		return 0
	}
	return *m.Threshold
}

// DefaultSpanPrompt is the system prompt of the span LLM in the image (config/technique_spans/prompt.md).
const DefaultSpanPrompt = "/etc/orchestrator/technique_spans/prompt.md"

// TechniqueSpans is the optional `spans` block: the sentence model scores sentences of English input, the LLM
// proposes phrases for other languages. Exec is LLM as an llm-style Execution, built at load for enabled analyzers.
type TechniqueSpans struct {
	SentenceModel          *TechniqueModel `yaml:"sentence_model"`
	LLM                    *SpanLLM        `yaml:"llm"`
	Timeout                time.Duration   `yaml:"timeout"`
	MaxSentences           *int            `yaml:"max_sentences"`
	MaxPhrasesPerTechnique *int            `yaml:"max_phrases_per_technique"`
	MaxInputChars          *int            `yaml:"max_input_chars"`
	Exec                   Execution       `yaml:"-"`
}

// SpanLLM is the OpenAI-compatible LLM of the spans block (same keys as the llm adapter).
type SpanLLM struct {
	BaseURL        string            `yaml:"base_url"`
	APIKey         string            `yaml:"api_key"`
	Model          string            `yaml:"model"`
	PromptFile     string            `yaml:"prompt_file"`
	Temperature    float64           `yaml:"temperature"`
	MaxTokens      int               `yaml:"max_tokens"`
	ResponseFormat string            `yaml:"response_format"`
	Headers        map[string]string `yaml:"headers"`
	ExtraBody      map[string]any    `yaml:"extra_body"`
}

func (s TechniqueSpans) MaxSentencesN() int { return intOr(s.MaxSentences, 20) }
func (s TechniqueSpans) MaxPhrasesN() int   { return intOr(s.MaxPhrasesPerTechnique, 5) }
func (s TechniqueSpans) MaxInputCharsN() int {
	return intOr(s.MaxInputChars, DefaultMaxInputChars)
}

// Defaults of the technique_classifier settings; tests build Executions without Load.
func (e Execution) ShortMaxWordsN() int { return intOr(e.ShortMaxWords, 80) }
func (e Execution) MaxSentencesN() int  { return intOr(e.MaxSentences, 20) }

// checkTechnique validates the technique_classifier settings and rejects them on other adapters.
func (a *Analyzer) checkTechnique() error {
	e := &a.Execution
	if e.Adapter != AdapterTechniqueClassifier {
		if e.ShortMaxWords != nil || e.MaxSentences != nil || e.Short != nil || e.Long != nil || e.Spans != nil {
			return fmt.Errorf("execution.short_max_words, max_sentences, short, long and spans are only allowed for adapter %s", AdapterTechniqueClassifier)
		}
		return nil
	}
	if e.Endpoint != "" || e.Method != "" || e.Handler != "" || e.PollInterval != 0 || len(e.Headers) > 0 {
		return fmt.Errorf("execution.endpoint, method, handler, poll_interval and headers are not allowed for adapter %s (set them in the short and long blocks)", e.Adapter)
	}
	if len(a.Input.Bindings) != 1 || !strings.HasPrefix(a.Input.Bindings["text"], "$analyzers.") {
		return fmt.Errorf("input.bindings for adapter %s must be exactly text: $analyzers.<id>", e.Adapter)
	}
	if e.ShortMaxWords != nil && *e.ShortMaxWords < 1 || e.MaxSentences != nil && *e.MaxSentences < 1 {
		return fmt.Errorf("execution.short_max_words and execution.max_sentences must be positive integers")
	}
	known := map[string]bool{}
	for _, n := range TechniqueNames {
		known[n] = true
	}
	models := []struct {
		name string
		m    *TechniqueModel
	}{{"short", e.Short}, {"long", e.Long}}
	if s := e.Spans; s != nil {
		switch {
		case s.LLM == nil:
			return fmt.Errorf("execution.spans.llm is required when execution.spans is set")
		case s.Timeout <= 0:
			return fmt.Errorf("execution.spans.timeout is required and must be positive")
		case s.MaxSentences != nil && *s.MaxSentences < 1 || s.MaxPhrasesPerTechnique != nil && *s.MaxPhrasesPerTechnique < 1 ||
			s.MaxInputChars != nil && *s.MaxInputChars < 1:
			return fmt.Errorf("execution.spans.max_sentences, max_phrases_per_technique and max_input_chars must be positive integers")
		}
		for _, k := range []string{"model", "messages"} {
			if _, ok := s.LLM.ExtraBody[k]; ok {
				return fmt.Errorf("execution.spans.llm.extra_body.%s is not allowed (it would replace the request's %s)", k, k)
			}
		}
		models = append(models, struct {
			name string
			m    *TechniqueModel
		}{"spans.sentence_model", s.SentenceModel})
	}
	for _, b := range models {
		switch {
		case b.m == nil:
			return fmt.Errorf("execution.%s is required for adapter %s", b.name, e.Adapter)
		case strings.TrimSpace(b.m.Endpoint) == "":
			return fmt.Errorf("execution.%s.endpoint is required", b.name)
		case b.m.Threshold == nil:
			return fmt.Errorf("execution.%s.threshold is required", b.name)
		case !inUnit(*b.m.Threshold):
			return fmt.Errorf("execution.%s.threshold must be between 0 and 1", b.name)
		}
		for n, t := range b.m.Thresholds {
			switch {
			case !known[n]:
				return fmt.Errorf("execution.%s.thresholds: %q is not a technique name", b.name, n)
			case !inUnit(t):
				return fmt.Errorf("execution.%s.thresholds.%s must be between 0 and 1", b.name, n)
			}
		}
	}
	return nil
}

func inUnit(f float64) bool { return f >= 0 && f <= 1 } // false for NaN

// expandTechnique expands ${VAR} in the endpoints and headers of an enabled analyzer and loads the span LLM.
func (e *Execution) expandTechnique(getenv func(string) (string, bool)) error {
	models := []struct {
		name string
		m    *TechniqueModel
	}{{"short", e.Short}, {"long", e.Long}}
	if e.Spans != nil {
		models = append(models, struct {
			name string
			m    *TechniqueModel
		}{"spans.sentence_model", e.Spans.SentenceModel})
	}
	for _, b := range models {
		var err error
		if b.m.Endpoint, err = expand(b.m.Endpoint, getenv); err != nil {
			return fmt.Errorf("execution.%s.endpoint: %w", b.name, err)
		}
		if strings.TrimSpace(b.m.Endpoint) == "" {
			return fmt.Errorf("execution.%s.endpoint is empty", b.name)
		}
		hdr := make(map[string]string, len(b.m.Headers))
		for k, v := range b.m.Headers {
			if hdr[k], err = expand(v, getenv); err != nil {
				return fmt.Errorf("execution.%s.headers.%s: %w", b.name, k, err)
			}
		}
		b.m.Headers = hdr
	}
	if e.Spans != nil {
		return e.Spans.loadLLM(getenv)
	}
	return nil
}

// loadLLM builds Exec from the llm block with its defaults and loads it like the llm adapter (expansion, prompt file).
func (s *TechniqueSpans) loadLLM(getenv func(string) (string, bool)) error {
	l := s.LLM
	x := Execution{Adapter: AdapterTechniqueClassifier, BaseURL: l.BaseURL, APIKey: l.APIKey, Model: l.Model,
		PromptFile: l.PromptFile, Temperature: l.Temperature, MaxTokens: l.MaxTokens, ResponseFormat: l.ResponseFormat,
		ExtraBody: l.ExtraBody, Headers: map[string]string{}}
	if x.PromptFile == "" {
		x.PromptFile = DefaultSpanPrompt
	}
	if x.MaxTokens == 0 {
		x.MaxTokens = 1000
	}
	if x.ResponseFormat == "" {
		x.ResponseFormat = FormatJSONSchema
	}
	if x.ExtraBody == nil {
		x.ExtraBody = map[string]any{}
	}
	for k, v := range l.Headers {
		var err error
		if x.Headers[k], err = expand(v, getenv); err != nil {
			return fmt.Errorf("execution.spans.llm.headers.%s: %w", k, err)
		}
	}
	if err := x.loadLLM(getenv, "execution.spans.llm."); err != nil {
		return err
	}
	s.Exec = x
	return nil
}
