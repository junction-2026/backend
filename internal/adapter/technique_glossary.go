package adapter

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/junction-2026/backend/internal/registry"
)

//go:embed technique_glossary.yaml
var glossaryYAML []byte

// glossaryLanguages are the explanation languages the glossary must cover; anything else falls back to en.
var glossaryLanguages = []string{"en", "fi", "sv", "de", "fr", "es", "ru", "uk"}

type glossaryEntry struct {
	Name       string `yaml:"name"`
	Definition string `yaml:"definition"`
}

type glossaryTemplates struct {
	WhySpan   string `yaml:"why_span"`
	WhyNoSpan string `yaml:"why_no_span"`
}

// glossary is the fixed plain-language glossary: Techniques[name][lang].
type glossary struct {
	Version    string                              `yaml:"version"`
	Templates  map[string]glossaryTemplates        `yaml:"templates"`
	Techniques map[string]map[string]glossaryEntry `yaml:"techniques"`
}

// techniqueGlossary is parsed at package init, so a broken artifact stops the process at startup.
var techniqueGlossary = func() *glossary {
	g, err := parseGlossary(glossaryYAML)
	if err != nil {
		panic("technique glossary: " + err.Error())
	}
	return g
}()

// parseGlossary decodes the artifact and requires every technique and LLM label, every language and valid templates.
func parseGlossary(b []byte) (*glossary, error) {
	var g glossary
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&g); err != nil {
		return nil, err
	}
	if strings.TrimSpace(g.Version) == "" {
		return nil, errors.New("version is empty")
	}
	langs := map[string]bool{}
	for _, l := range glossaryLanguages {
		langs[l] = true
		t, ok := g.Templates[l]
		switch {
		case !ok:
			return nil, fmt.Errorf("templates: language %s is missing", l)
		case strings.Count(t.WhySpan, "{name}") != 1 || strings.Count(t.WhySpan, "{span}") != 1:
			return nil, fmt.Errorf("templates.%s.why_span needs exactly one {name} and one {span}", l)
		case strings.Count(t.WhyNoSpan, "{name}") != 1 || strings.Contains(t.WhyNoSpan, "{span}"):
			return nil, fmt.Errorf("templates.%s.why_no_span needs exactly one {name} and no {span}", l)
		}
	}
	for l := range g.Templates {
		if !langs[l] {
			return nil, fmt.Errorf("templates: unknown language %s", l)
		}
	}
	names := map[string]bool{}
	for _, n := range append(append([]string{}, registry.TechniqueNames...), llmTechniqueLabels...) {
		names[n] = true
		for _, l := range glossaryLanguages {
			if e := g.Techniques[n][l]; strings.TrimSpace(e.Name) == "" || strings.TrimSpace(e.Definition) == "" {
				return nil, fmt.Errorf("techniques.%s.%s needs a non-empty name and definition", n, l)
			}
		}
		for l := range g.Techniques[n] {
			if !langs[l] {
				return nil, fmt.Errorf("techniques.%s: unknown language %s", n, l)
			}
		}
	}
	for n := range g.Techniques {
		if !names[n] {
			return nil, fmt.Errorf("techniques: unknown technique %s", n)
		}
	}
	return &g, nil
}

// explanationLanguage is the translation's source_language when the glossary has it, else en.
func explanationLanguage(source string) string {
	if l := strings.ToLower(strings.TrimSpace(source)); techniqueGlossary.Templates[l].WhySpan != "" {
		return l
	}
	return "en"
}

// plainLanguage is the PlainLanguage object of a Detection.
type plainLanguage struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Why        string `json:"why"`
}

// explain fills plain_language and every span's why from the glossary templates by literal, single-pass
// substitution (text inside a span is never substituted again).
func explain(d *detectedTechnique, lang string) {
	e := techniqueGlossary.Techniques[d.Name][lang]
	t := techniqueGlossary.Templates[lang]
	for i := range d.Spans {
		d.Spans[i].Why = strings.NewReplacer("{name}", e.Name, "{span}", d.Spans[i].Text).Replace(t.WhySpan)
	}
	why := strings.Replace(t.WhyNoSpan, "{name}", e.Name, 1)
	if len(d.Spans) > 0 {
		why = d.Spans[0].Why
	}
	d.PlainLanguage = plainLanguage{e.Name, e.Definition, why}
}
