package registry

import (
	"fmt"
	"strings"
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

// Defaults of the technique_classifier settings; tests build Executions without Load.
func (e Execution) ShortMaxWordsN() int { return intOr(e.ShortMaxWords, 80) }
func (e Execution) MaxSentencesN() int  { return intOr(e.MaxSentences, 20) }

// checkTechnique validates the technique_classifier settings and rejects them on other adapters.
func (a *Analyzer) checkTechnique() error {
	e := &a.Execution
	if e.Adapter != AdapterTechniqueClassifier {
		if e.ShortMaxWords != nil || e.MaxSentences != nil || e.Short != nil || e.Long != nil {
			return fmt.Errorf("execution.short_max_words, max_sentences, short and long are only allowed for adapter %s", AdapterTechniqueClassifier)
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
	for _, b := range []struct {
		name string
		m    *TechniqueModel
	}{{"short", e.Short}, {"long", e.Long}} {
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

// expandTechnique expands ${VAR} in the endpoints and headers of an enabled analyzer.
func (e *Execution) expandTechnique(getenv func(string) (string, bool)) error {
	for _, b := range []struct {
		name string
		m    *TechniqueModel
	}{{"short", e.Short}, {"long", e.Long}} {
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
	return nil
}
