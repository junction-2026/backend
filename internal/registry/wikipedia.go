package registry

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// DefaultWikipediaBaseURLTemplate is the MediaWiki API of {lang}.wikipedia.org; execution.base_url_template overrides it.
const DefaultWikipediaBaseURLTemplate = "https://{lang}.wikipedia.org/w/api.php"

// Wikipedia is the validated wikipedia configuration (defaults applied).
type Wikipedia struct {
	MinCheckWorthiness                                  float64
	MaxClaims, MaxHits, MaxPassageChars, MaxConcurrency int
	CacheDir, UserAgent, BaseURLTemplate                string
	CacheTTL                                            time.Duration
}

// checkWikipedia rejects the wikipedia-only keys on other adapters and validates and defaults them for wikipedia.
func (a *Analyzer) checkWikipedia(opt Options) error {
	e := &a.Execution
	if e.Adapter == AdapterTermContext {
		return nil // validated in checkTermContext, which shares wikiTransport
	}
	if e.Adapter != AdapterWikipedia {
		if e.MaxHits != nil || e.MaxPassageChars != nil || e.CacheDir != "" || e.UserAgent != "" || e.BaseURLTemplate != "" {
			return fmt.Errorf("execution.max_hits, max_passage_chars, cache_dir, user_agent and base_url_template are only allowed for adapter %s", AdapterWikipedia)
		}
		return nil
	}
	only := *e
	only.Adapter, only.Timeout, only.MinCheckWorthiness, only.MaxClaims, only.MaxConcurrency, only.CacheTTL = "", 0, nil, nil, nil, nil
	only.MaxHits, only.MaxPassageChars, only.CacheDir, only.UserAgent, only.BaseURLTemplate = nil, nil, "", "", ""
	if !reflect.ValueOf(only).IsZero() { // every other execution key belongs to another adapter
		return fmt.Errorf("only execution.min_check_worthiness, max_claims, max_hits, max_passage_chars, max_concurrency, cache_dir, cache_ttl, user_agent, base_url_template and timeout are allowed for adapter %s", e.Adapter)
	}
	if a.Trigger != TriggerOnDeps {
		return fmt.Errorf("adapter %s requires trigger %s", e.Adapter, TriggerOnDeps)
	}
	if !contains(a.DependsOn, "topic_extraction") {
		return fmt.Errorf("depends_on must include topic_extraction for adapter %s", e.Adapter)
	}
	if !strings.HasPrefix(a.Input.Bindings["extraction"], "$analyzers.") {
		return fmt.Errorf("input.bindings.extraction must be $analyzers.<id> for adapter %s", e.Adapter)
	}
	for name, src := range a.Input.Bindings {
		if name != "extraction" && !(name == "translation" && strings.HasPrefix(src, "$analyzers.")) {
			return fmt.Errorf("input.bindings for adapter %s accept only extraction: $analyzers.<id> and translation: $analyzers.<id>", e.Adapter)
		}
	}
	w := &e.Wiki
	w.MinCheckWorthiness = 0.4
	if p := e.MinCheckWorthiness; p != nil {
		if *p < 0 || *p > 1 {
			return fmt.Errorf("execution.min_check_worthiness must be between 0 and 1")
		}
		w.MinCheckWorthiness = *p
	}
	for _, f := range []struct {
		name     string
		in       *int
		out      *int
		def, top int
	}{
		{"max_claims", e.MaxClaims, &w.MaxClaims, 3, 20}, {"max_hits", e.MaxHits, &w.MaxHits, 5, 20}, // 20: MediaWiki's extract limit per request
		{"max_passage_chars", e.MaxPassageChars, &w.MaxPassageChars, 1500, 100000}, {"max_concurrency", e.MaxConcurrency, &w.MaxConcurrency, 2, 20},
	} {
		if *f.out = f.def; f.in != nil {
			if *f.in < 1 || *f.in > f.top {
				return fmt.Errorf("execution.%s must be between 1 and %d", f.name, f.top)
			}
			*f.out = *f.in
		}
	}
	if err := a.wikiTransport(opt); err != nil {
		return err
	}
	if e.Timeout == 0 {
		e.Timeout = 20 * time.Second
	}
	return nil
}

// wikiTransport validates the cache, base URL and User-Agent keys that wikipedia and term_context share.
func (a *Analyzer) wikiTransport(opt Options) error {
	e, w := &a.Execution, &a.Execution.Wiki
	if w.CacheTTL = 168 * time.Hour; e.CacheTTL != nil {
		if *e.CacheTTL <= 0 {
			return fmt.Errorf("execution.cache_ttl must be a positive duration")
		}
		w.CacheTTL = *e.CacheTTL
	}
	if w.CacheDir = e.CacheDir; strings.TrimSpace(w.CacheDir) == "" {
		w.CacheDir = filepath.Join("var", "wikipedia")
		if d, _ := opt.Getenv("DATA_DIR"); strings.TrimSpace(d) != "" {
			w.CacheDir = filepath.Join(d, "wikipedia")
		}
	}
	if w.BaseURLTemplate = e.BaseURLTemplate; w.BaseURLTemplate == "" {
		w.BaseURLTemplate = DefaultWikipediaBaseURLTemplate
	}
	if !strings.Contains(w.BaseURLTemplate, "{lang}") {
		return fmt.Errorf("execution.base_url_template must contain {lang}")
	}
	if w.UserAgent = strings.TrimSpace(e.UserAgent); w.UserAgent == "" {
		return fmt.Errorf("execution.user_agent is required for adapter %s (a contact URL or e-mail, per the Wikimedia User-Agent policy)", e.Adapter)
	}
	return nil
}

// expandWikipedia expands ${VAR} in the wikipedia strings of an enabled analyzer and re-checks them.
func (e *Execution) expandWikipedia(getenv func(string) (string, bool)) error {
	w := &e.Wiki
	for _, f := range []struct {
		name string
		v    *string
	}{{"user_agent", &w.UserAgent}, {"base_url_template", &w.BaseURLTemplate}, {"cache_dir", &w.CacheDir}} {
		var err error
		if *f.v, err = expand(*f.v, getenv); err != nil {
			return fmt.Errorf("execution.%s: %w", f.name, err)
		}
		if *f.v = strings.TrimSpace(*f.v); *f.v == "" {
			return fmt.Errorf("execution.%s is empty; it is required for an enabled %s analyzer", f.name, e.Adapter)
		}
	}
	if !strings.Contains(w.BaseURLTemplate, "{lang}") {
		return fmt.Errorf("execution.base_url_template must contain {lang}")
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}
