package registry

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// AdapterTermContext quotes how a post's loaded or unfamiliar terms are used in Wikipedia and news (shared term-context-result).
const AdapterTermContext = "term_context"

// MaxTermsN, InPostMaxCharsN, QuoteMaxCharsN and ElsewhereMaxN are the term_context limits with their defaults.
func (e Execution) MaxTermsN() int       { return intOr(e.MaxTerms, 5) }
func (e Execution) InPostMaxCharsN() int { return intOr(e.InPostMaxChars, 300) }
func (e Execution) QuoteMaxCharsN() int  { return intOr(e.QuoteMaxChars, 400) }
func (e Execution) ElsewhereMaxN() int   { return intOr(e.ElsewhereMax, 3) }

// checkTermContext validates the term_context settings, applies their defaults and rejects its own keys on other adapters.
func (a *Analyzer) checkTermContext(opt Options) error {
	e := &a.Execution
	if e.Adapter != AdapterTermContext {
		if e.MaxTerms != nil || e.InPostMaxChars != nil || e.QuoteMaxChars != nil || e.ElsewhereMax != nil {
			return fmt.Errorf("execution.max_terms, in_post_max_chars, quote_max_chars and elsewhere_max are only allowed for adapter %s", AdapterTermContext)
		}
		return nil
	}
	only := *e
	only.Adapter, only.Timeout, only.BaseURL, only.APIKey, only.Model = "", 0, "", "", ""
	only.Temperature, only.MaxTokens, only.Headers, only.ResponseFormat, only.ExtraBody = 0, 0, nil, "", nil
	only.RetryInvalidOutput, only.RetryMaxTokens, only.RetryModel, only.RetryMinRemaining = nil, nil, "", nil
	only.MaxInputChars, only.MaxTerms, only.InPostMaxChars, only.QuoteMaxChars, only.ElsewhereMax = nil, nil, nil, nil, nil
	only.UserAgent, only.CacheDir, only.BaseURLTemplate, only.CacheTTL, only.MaxConcurrency = "", "", "", nil, nil
	if !reflect.ValueOf(only).IsZero() { // every other execution key belongs to another adapter
		return fmt.Errorf("only execution.base_url, api_key, model, temperature, max_tokens, headers, response_format, extra_body, retry_invalid_output, retry_max_tokens, retry_model, retry_min_remaining, max_input_chars, max_terms, in_post_max_chars, quote_max_chars, elsewhere_max, user_agent, cache_dir, cache_ttl, base_url_template, max_concurrency and timeout are allowed for adapter %s", e.Adapter)
	}
	if a.Trigger != TriggerOnDeps {
		return fmt.Errorf("adapter %s requires trigger %s", e.Adapter, TriggerOnDeps)
	}
	for _, d := range []string{"topic_extraction", "translation", "news_search"} {
		if !contains(a.DependsOn, d) {
			return fmt.Errorf("depends_on must include %s for adapter %s", d, e.Adapter)
		}
	}
	if len(a.Input.Bindings) != 3 {
		return fmt.Errorf("input.bindings for adapter %s must be exactly extraction, translation and news: $analyzers.<id>", e.Adapter)
	}
	for _, name := range []string{"extraction", "translation", "news"} {
		if !strings.HasPrefix(a.Input.Bindings[name], "$analyzers.") {
			return fmt.Errorf("input.bindings for adapter %s must be exactly extraction, translation and news: $analyzers.<id>", e.Adapter)
		}
	}
	for name, p := range map[string]*int{"max_terms": e.MaxTerms, "in_post_max_chars": e.InPostMaxChars, "quote_max_chars": e.QuoteMaxChars,
		"elsewhere_max": e.ElsewhereMax, "max_input_chars": e.MaxInputChars} {
		if p != nil && *p < 1 {
			return fmt.Errorf("execution.%s must be a positive integer", name)
		}
	}
	switch {
	case e.MaxTokens < 0:
		return fmt.Errorf("execution.max_tokens must be a positive integer")
	case e.RetryMaxTokens != nil && *e.RetryMaxTokens < 1:
		return fmt.Errorf("execution.retry_max_tokens must be a positive integer")
	case e.RetryMinRemaining != nil && *e.RetryMinRemaining < 0:
		return fmt.Errorf("execution.retry_min_remaining must not be negative")
	}
	for _, k := range []string{"model", "messages"} {
		if _, ok := e.ExtraBody[k]; ok {
			return fmt.Errorf("execution.extra_body.%s is not allowed (it would replace the request's %s)", k, k)
		}
	}
	w := &e.Wiki
	if w.MaxConcurrency = 2; e.MaxConcurrency != nil {
		if *e.MaxConcurrency < 1 || *e.MaxConcurrency > 20 {
			return fmt.Errorf("execution.max_concurrency must be between 1 and 20")
		}
		w.MaxConcurrency = *e.MaxConcurrency
	}
	if err := a.wikiTransport(opt); err != nil {
		return err
	}
	if e.MaxTokens == 0 {
		e.MaxTokens = 1000
	}
	if e.ResponseFormat == "" {
		e.ResponseFormat = FormatJSONSchema
	}
	if e.ExtraBody == nil {
		e.ExtraBody = map[string]any{}
	}
	if e.Timeout == 0 {
		e.Timeout = 60 * time.Second
	}
	return nil
}
