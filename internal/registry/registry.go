// Package registry loads and validates the analyzer registry (ANALYZERS_CONFIG).
package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/outlets"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"gopkg.in/yaml.v3"
)

var messagePrinter = message.NewPrinter(language.English)

const (
	TriggerOnDeps   = "on_dependencies_succeeded"
	TriggerAfterAll = "after_all_analyzers_settled"

	AdapterHTTPSync  = "http_sync"
	AdapterHTTPAsync = "http_async"
	AdapterCustom    = "custom"
	AdapterLLM       = "llm"

	AdapterPropagandaDetection = "propaganda_detection"
	AdapterBotDetection        = "bot_detection"
	AdapterMistralTranslation  = "mistral_translation"
	AdapterFactCheck           = "fact_check"
	AdapterNewsSearch          = "news_search"
	AdapterWikipedia           = "wikipedia"

	DefaultFactCheckBaseURL = "https://factchecktools.googleapis.com/v1alpha1"

	DefaultMistralEndpoint = "https://api.mistral.ai/v1/chat/completions"
	DefaultMistralModel    = "mistral-small-latest"
	DefaultMaxInputChars   = 20000
	DefaultEnglishMinWords = 8
	DefaultEnglishRatio    = 0.25

	// execution.task of the llm adapter; absent means final_synthesis.
	TaskFinalSynthesis  = "final_synthesis"
	TaskTopicExtraction = "topic_extraction"
)

var (
	idRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

type AppliesTo struct {
	ContentTypes      []string `yaml:"content_types"`
	RequiresSourceURL bool     `yaml:"requires_source_url"`
	MinContentLength  int      `yaml:"min_content_length"`
}

type Input struct {
	Bindings map[string]string `yaml:"bindings"`
}

type Execution struct {
	Adapter      string            `yaml:"adapter"`
	Endpoint     string            `yaml:"endpoint"`
	Method       string            `yaml:"method"`
	Headers      map[string]string `yaml:"headers"`
	Timeout      time.Duration     `yaml:"timeout"`
	PollInterval time.Duration     `yaml:"poll_interval"`
	Handler      string            `yaml:"handler"`

	// APIKey and Model are shared by llm and mistral_translation; BaseURL..ExtraBody are llm only
	BaseURL        string         `yaml:"base_url"`
	APIKey         string         `yaml:"api_key"`
	Model          string         `yaml:"model"`
	Task           string         `yaml:"task"` // llm only
	PromptFile     string         `yaml:"prompt_file"`
	Temperature    float64        `yaml:"temperature"`
	MaxTokens      int            `yaml:"max_tokens"`
	ResponseFormat string         `yaml:"response_format"`
	ExtraBody      map[string]any `yaml:"extra_body"`
	Prompt         string         `yaml:"-"` // prompt_file content, read once at load

	// llm only: one reply-level retry of a cut-off, non-JSON or schema-failing reply; see the Retry* methods for defaults
	RetryInvalidOutput *bool          `yaml:"retry_invalid_output"`
	RetryMaxTokens     *int           `yaml:"retry_max_tokens"`
	RetryModel         string         `yaml:"retry_model"`
	RetryMinRemaining  *time.Duration `yaml:"retry_min_remaining"`

	MaxInputChars *int `yaml:"max_input_chars"` // mistral_translation, llm topic_extraction and llm_techniques only

	// llm_techniques only (base_url, api_key, model, prompt_file, temperature, max_tokens and headers are shared)
	MinConfidence       *float64 `yaml:"min_confidence"`
	MaxFindingsPerLabel *int     `yaml:"max_findings_per_label"`

	// mistral_translation only: skip the Mistral call for text that is clearly English
	SkipEnglish     *bool    `yaml:"skip_english"`
	EnglishMinWords *int     `yaml:"english_min_words"`
	EnglishMinRatio *float64 `yaml:"english_min_ratio"`

	// news_search only (api_key and base_url are shared); News holds the validated values with defaults.
	Languages      []string       `yaml:"languages"`
	Translate      *NewsTranslate `yaml:"translate"`
	MaxTopics      *int           `yaml:"max_topics"`
	MaxNews        *int           `yaml:"max_news"`
	Num            *int           `yaml:"num"`
	MinResults     *int           `yaml:"min_results"`
	PerDomain      *int           `yaml:"per_domain"`
	MaxResults     *int           `yaml:"max_results"`
	MaxConcurrency *int           `yaml:"max_concurrency"`
	CacheTTL       *time.Duration `yaml:"cache_ttl"`
	News           NewsSearch     `yaml:"-"`

	// fact_check (BaseURL, APIKey, MaxResults, MaxConcurrency and CacheTTL are shared); MaxClaims is also the topic_extraction cap
	// technique_classifier only: Technique/Social are the NCI models, selected by word count
	SocialMaxWords    *int            `yaml:"social_max_words"`
	TechniqueMinWords *int            `yaml:"technique_min_words"`
	Technique         *NCIModel       `yaml:"technique"`
	Social            *NCIModel       `yaml:"social"`
	Spans             *TechniqueSpans `yaml:"spans"`
	StrongMargin      any             `yaml:"strong_margin"` // any: a non-number is reported naming the file and analyzer id
	// keys of the earlier technique_classifier, decoded only to reject them naming their replacement
	OldShortMaxWords any `yaml:"short_max_words"`
	OldMaxSentences  any `yaml:"max_sentences"`
	OldShort         any `yaml:"short"`
	OldLong          any `yaml:"long"`

	MinCheckWorthiness *float64 `yaml:"min_check_worthiness"`
	MaxClaims          *int     `yaml:"max_claims"`
	PageSize           *int     `yaml:"page_size"`
	MaxAgeDays         *int     `yaml:"max_age_days"`

	// wikipedia only (min_check_worthiness, max_claims, max_concurrency, cache_ttl and timeout are shared); Wiki holds the validated values.
	MaxHits         *int      `yaml:"max_hits"`
	MaxPassageChars *int      `yaml:"max_passage_chars"`
	CacheDir        string    `yaml:"cache_dir"`
	UserAgent       string    `yaml:"user_agent"`
	BaseURLTemplate string    `yaml:"base_url_template"`
	Wiki            Wikipedia `yaml:"-"`

	// term_context only (the LLM keys, max_input_chars and the wikipedia transport keys are shared); defaults in term_context.go
	MaxTerms       *int `yaml:"max_terms"`
	InPostMaxChars *int `yaml:"in_post_max_chars"`
	QuoteMaxChars  *int `yaml:"quote_max_chars"`
	ElsewhereMax   *int `yaml:"elsewhere_max"`

	// source_info only: the outlet table file; Outlets is read from it at load when the analyzer is enabled.
	OutletsFile string         `yaml:"outlets_file"`
	Outlets     *outlets.Table `yaml:"-"`
}

// NewsTranslate configures the Mistral translation of news_search queries.
type NewsTranslate struct {
	APIKey   string `yaml:"api_key"`
	Model    string `yaml:"model"`
	Endpoint string `yaml:"endpoint"`
}

// NewsSearch is the validated news_search configuration (defaults applied).
type NewsSearch struct {
	Languages                                                                  []string // "source" or lower-case ISO 639-1
	Translate                                                                  NewsTranslate
	MaxTopics, MaxNews, Num, MinResults, PerDomain, MaxResults, MaxConcurrency int
	CacheTTL                                                                   time.Duration
}

func intOr(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

// Defaults of the fact_check and extractor settings; tests build Executions without Load.
func (e Execution) ExtractorMaxClaims() int { return intOr(e.MaxClaims, 5) }
func (e Execution) FactCheckMaxClaims() int { return intOr(e.MaxClaims, 3) }
func (e Execution) PageSizeN() int          { return intOr(e.PageSize, 5) }
func (e Execution) MaxAgeDaysN() int        { return intOr(e.MaxAgeDays, 1825) }
func (e Execution) MaxResultsN() int        { return intOr(e.MaxResults, 10) }
func (e Execution) MaxConcurrencyN() int    { return intOr(e.MaxConcurrency, 4) }
func (e Execution) MinWorthiness() float64 {
	if e.MinCheckWorthiness == nil {
		return 0.4
	}
	return *e.MinCheckWorthiness
}
func (e Execution) CacheTTLD() time.Duration {
	if e.CacheTTL == nil || *e.CacheTTL <= 0 {
		return 24 * time.Hour
	}
	return *e.CacheTTL
}

// checkFactCheck validates the fact_check settings of one analyzer.
func (a *Analyzer) checkFactCheck() error {
	e := &a.Execution
	switch {
	case e.Method != "" || e.Handler != "" || e.PollInterval != 0 || e.Endpoint != "":
		return fmt.Errorf("execution.method, execution.handler, execution.poll_interval and execution.endpoint are not allowed for adapter %s", e.Adapter)
	case len(a.Input.Bindings) != 1 || !strings.HasPrefix(a.Input.Bindings["extraction"], "$analyzers."):
		return fmt.Errorf("input.bindings for adapter %s must be exactly extraction: $analyzers.<id>", e.Adapter)
	case e.MinCheckWorthiness != nil && (*e.MinCheckWorthiness < 0 || *e.MinCheckWorthiness > 1):
		return fmt.Errorf("execution.min_check_worthiness must be between 0 and 1")
	case e.CacheTTL != nil && *e.CacheTTL <= 0:
		return fmt.Errorf("execution.cache_ttl must be positive")
	}
	for name, p := range map[string]*int{"max_claims": e.MaxClaims, "page_size": e.PageSize, "max_age_days": e.MaxAgeDays,
		"max_results": e.MaxResults, "max_concurrency": e.MaxConcurrency} {
		if p != nil && *p < 1 {
			return fmt.Errorf("execution.%s must be a positive integer", name)
		}
	}
	return nil
}

// MaxChars is max_input_chars with its default.
func (e Execution) MaxChars() int {
	if e.MaxInputChars == nil {
		return DefaultMaxInputChars
	}
	return *e.MaxInputChars
}

// RetryEnabled is retry_invalid_output (default true).
func (e Execution) RetryEnabled() bool { return e.RetryInvalidOutput == nil || *e.RetryInvalidOutput }

// RetryMaxTokensN is retry_max_tokens (default twice max_tokens).
func (e Execution) RetryMaxTokensN() int { return intOr(e.RetryMaxTokens, 2*e.MaxTokens) }

// RetryMinRemainingD is retry_min_remaining (default 5s).
func (e Execution) RetryMinRemainingD() time.Duration {
	if e.RetryMinRemaining == nil {
		return 5 * time.Second
	}
	return *e.RetryMinRemaining
}

// SkipsEnglish is skip_english with its default (true).
func (e Execution) SkipsEnglish() bool { return e.SkipEnglish == nil || *e.SkipEnglish }

// EnglishWords is english_min_words with its default.
func (e Execution) EnglishWords() int {
	if e.EnglishMinWords == nil {
		return DefaultEnglishMinWords
	}
	return *e.EnglishMinWords
}

// EnglishRatio is english_min_ratio with its default.
func (e Execution) EnglishRatio() float64 {
	if e.EnglishMinRatio == nil {
		return DefaultEnglishRatio
	}
	return *e.EnglishMinRatio
}

// LLM response_format modes.
const (
	FormatJSONSchema = "json_schema"
	FormatJSONObject = "json_object"
	FormatNone       = "none"
)

const (
	DefaultPromptFile        = "/etc/orchestrator/finalizer/prompt.md"
	DefaultExtractorPrompt   = "/etc/orchestrator/extractor/prompt.md"
	defaultExtractorMaxToken = 2000
)

// TopicExtraction reports whether the execution is the llm topic extraction task.
func (e Execution) TopicExtraction() bool {
	return e.Adapter == AdapterLLM && e.Task == TaskTopicExtraction
}

// LLMInput opts an analyzer's run into the llm finalizer's input.
type LLMInput struct {
	Include     bool   `yaml:"include"`
	Description string `yaml:"description"`
}

// UI is the optional `ui` block of an analyzer: progress presentation only. It is never exposed by GET /analyzers.
type UI struct {
	Hidden      *bool  `yaml:"hidden"`       // default false; text_stats defaults to true
	ActivityKey string `yaml:"activity_key"` // default: the analyzer id
}

// HiddenFromProgress reports whether the analyzer stays out of the plan, progress, previews and activities.
func (a *Analyzer) HiddenFromProgress() bool {
	if a.UI.Hidden != nil {
		return *a.UI.Hidden
	}
	return a.ID == "text_stats"
}

// ActivityKey is ui.activity_key or the analyzer id.
func (a *Analyzer) ActivityKey() string {
	if a.UI.ActivityKey != "" {
		return a.UI.ActivityKey
	}
	return a.ID
}

type Output struct {
	Schema map[string]any `yaml:"schema"`
}

// Analyzer is one validated registry definition. Execution settings may carry
// credentials: never log or expose an Analyzer.
type Analyzer struct {
	ID        string    `yaml:"id"`
	Name      string    `yaml:"name"`
	Category  string    `yaml:"category"`
	Enabled   bool      `yaml:"-"`
	Trigger   string    `yaml:"trigger"`
	DependsOn []string  `yaml:"depends_on"`
	AppliesTo AppliesTo `yaml:"applies_to"`
	Input     Input     `yaml:"input"`
	Execution Execution `yaml:"execution"`
	Output    Output    `yaml:"output"`
	LLM       *LLMInput `yaml:"llm"`
	UI        UI        `yaml:"ui"`

	file   string
	schema *jsonschema.Schema
}

type rawAnalyzer struct {
	Analyzer `yaml:",inline"`
	Enabled  *bool `yaml:"enabled"`
}

// Options of Load.
type Options struct {
	// Getenv resolves ${VAR}; defaults to os.LookupEnv.
	Getenv func(string) (string, bool)
	// HandlerExists tells whether an in-process custom handler exists.
	HandlerExists func(string) bool

	outlets map[string]*outlets.Table // tables loaded by this Load, shared by file
}

// Registry is an immutable, validated set of analyzers.
type Registry struct {
	all  []*Analyzer // ordered by id
	byID map[string]*Analyzer
}

func (r *Registry) All() []*Analyzer        { return r.all }
func (r *Registry) Get(id string) *Analyzer { return r.byID[id] }

// Finalizer returns the enabled after_all_analyzers_settled analyzer, or nil.
func (r *Registry) Finalizer() *Analyzer {
	for _, a := range r.all {
		if a.Trigger == TriggerAfterAll && a.Enabled {
			return a
		}
	}
	return nil
}

// Closure returns ids plus their transitive dependencies.
func (r *Registry) Closure(ids []string) map[string]bool {
	out := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if out[id] {
			return
		}
		out[id] = true
		for _, d := range r.byID[id].DependsOn {
			visit(d)
		}
	}
	for _, id := range ids {
		visit(id)
	}
	return out
}

// ValidateOutput checks raw against the analyzer's output schema (if any) and
// returns an error naming the first violated path.
func (a *Analyzer) ValidateOutput(raw json.RawMessage) error {
	if a.schema == nil {
		return nil
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("output is not valid JSON")
	}
	err = a.schema.Validate(v)
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if errors.As(err, &ve) {
		for len(ve.Causes) > 0 {
			ve = ve.Causes[0]
		}
		return fmt.Errorf("output violates schema at /%s: %s", strings.Join(ve.InstanceLocation, "/"), ve.ErrorKind.LocalizedString(messagePrinter))
	}
	return fmt.Errorf("output violates schema")
}

// Load reads path (a YAML file or a directory of *.yaml/*.yml files). Every
// error names the file and, where known, the analyzer id.
func Load(path string, opt Options) (*Registry, error) {
	if opt.Getenv == nil {
		opt.Getenv = os.LookupEnv
	}
	if opt.HandlerExists == nil {
		opt.HandlerExists = func(string) bool { return false }
	}
	opt.outlets = map[string]*outlets.Table{}
	files, err := listFiles(path)
	if err != nil {
		return nil, err
	}
	reg := &Registry{byID: map[string]*Analyzer{}}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		var doc struct {
			Analyzers []rawAnalyzer `yaml:"analyzers"`
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&doc); err != nil && !errors.Is(err, io.EOF) { // EOF: empty file, caught below
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		if len(doc.Analyzers) == 0 {
			return nil, fmt.Errorf("%s: no analyzers defined (top-level 'analyzers' list is empty or missing)", f)
		}
		for i := range doc.Analyzers {
			a := doc.Analyzers[i].Analyzer
			a.Enabled = doc.Analyzers[i].Enabled == nil || *doc.Analyzers[i].Enabled
			a.file = f
			if err := a.normalize(opt); err != nil {
				return nil, a.errf("%v", err)
			}
			if prev, dup := reg.byID[a.ID]; dup {
				return nil, a.errf("duplicate analyzer id (also defined in %s)", prev.file)
			}
			reg.byID[a.ID] = &a
			reg.all = append(reg.all, &a)
		}
	}
	if len(reg.all) == 0 {
		return nil, fmt.Errorf("%s: no analyzer files found", path)
	}
	sort.Slice(reg.all, func(i, j int) bool { return reg.all[i].ID < reg.all[j].ID })
	if err := reg.validateGraph(); err != nil {
		return nil, err
	}
	if err := reg.checkSourceInfoGraph(); err != nil {
		return nil, err
	}
	return reg, nil
}

func listFiles(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("analyzers config: %w", err)
	}
	if !st.IsDir() {
		return []string{path}, nil
	}
	ents, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("analyzers config: %w", err)
	}
	var files []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && (strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml")) {
			files = append(files, filepath.Join(path, n))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (a *Analyzer) errf(format string, args ...any) error {
	id := a.ID
	if id == "" {
		id = "<missing id>"
	}
	return fmt.Errorf("%s: analyzer %q: %s", a.file, id, fmt.Sprintf(format, args...))
}

// normalize applies defaults, expands ${VAR} and validates this analyzer alone.
func (a *Analyzer) normalize(opt Options) error {
	if !idRe.MatchString(a.ID) {
		return fmt.Errorf("id must match %s", idRe)
	}
	if a.Name == "" {
		a.Name = a.ID
	}
	switch a.Trigger {
	case "":
		a.Trigger = TriggerOnDeps
	case TriggerOnDeps, TriggerAfterAll:
	default:
		return fmt.Errorf("unknown trigger %q", a.Trigger)
	}
	if a.DependsOn == nil {
		a.DependsOn = []string{}
	}
	if k := a.UI.ActivityKey; k != "" && !idRe.MatchString(k) {
		return fmt.Errorf("ui.activity_key must match %s", idRe)
	}
	e := &a.Execution
	if e.Adapter == AdapterPropagandaDetection {
		// fixed request shape: checked before defaults fill method and poll_interval
		switch {
		case e.Method != "" && e.Method != "POST":
			return fmt.Errorf("execution.method must be POST or absent for adapter %s", e.Adapter)
		case e.Handler != "" || e.PollInterval != 0:
			return fmt.Errorf("execution.handler and execution.poll_interval are not allowed for adapter %s", e.Adapter)
		}
		for name, src := range a.Input.Bindings { // only `text`, from the job input or a dependency
			if name != "text" || (src != "$input" && !strings.HasPrefix(src, "$analyzers.")) {
				return fmt.Errorf("input.bindings for adapter %s accept only text: $input or text: $analyzers.<id>", e.Adapter)
			}
		}
	}
	if e.Adapter == AdapterBotDetection { // fixed request shape; the text is always the job's original text
		switch {
		case e.Method != "" && e.Method != "POST":
			return fmt.Errorf("execution.method must be POST or absent for adapter %s", e.Adapter)
		case e.Handler != "" || e.PollInterval != 0:
			return fmt.Errorf("execution.handler and execution.poll_interval are not allowed for adapter %s", e.Adapter)
		}
		for name, src := range a.Input.Bindings {
			if name != "content" || src != "$input" {
				return fmt.Errorf("input.bindings for adapter %s accept only content: $input", e.Adapter)
			}
		}
	}
	if err := a.checkTechnique(); err != nil {
		return err
	}
	if err := a.checkSourceInfo(); err != nil {
		return err
	}
	if err := a.checkWikipedia(opt); err != nil {
		return err
	}
	if err := a.checkLLMTechniques(); err != nil {
		return err
	}
	if err := a.checkTermContext(opt); err != nil {
		return err
	}
	if e.Adapter == AdapterMistralTranslation {
		switch {
		case e.Method != "" || e.Handler != "" || e.PollInterval != 0:
			return fmt.Errorf("execution.method, execution.handler and execution.poll_interval are not allowed for adapter %s", e.Adapter)
		case len(a.Input.Bindings) > 0:
			return fmt.Errorf("input.bindings are not allowed for adapter %s (it translates the job input)", e.Adapter)
		case e.APIKey == "":
			return fmt.Errorf("execution.api_key is required for adapter %s", e.Adapter)
		case e.MaxInputChars != nil && *e.MaxInputChars <= 0:
			return fmt.Errorf("execution.max_input_chars must be a positive integer")
		case e.EnglishMinWords != nil && *e.EnglishMinWords < 1:
			return fmt.Errorf("execution.english_min_words must be a positive integer")
		case e.EnglishMinRatio != nil && !(*e.EnglishMinRatio > 0 && *e.EnglishMinRatio <= 1):
			return fmt.Errorf("execution.english_min_ratio must be greater than 0 and at most 1")
		}
		if e.Endpoint == "" {
			e.Endpoint = DefaultMistralEndpoint
		}
		if e.Model == "" {
			e.Model = DefaultMistralModel
		}
		if e.Timeout == 0 {
			e.Timeout = 60 * time.Second
		}
	}
	if e.Adapter != AdapterMistralTranslation && (e.SkipEnglish != nil || e.EnglishMinWords != nil || e.EnglishMinRatio != nil) {
		return fmt.Errorf("execution.skip_english, english_min_words and english_min_ratio are only allowed for adapter %s", AdapterMistralTranslation)
	}
	if e.Adapter == AdapterNewsSearch {
		if err := a.normalizeNewsSearch(); err != nil {
			return err
		}
	} else if e.hasNewsOnlyKeys() || (e.hasSharedKeys() && e.Adapter != AdapterFactCheck && e.Adapter != AdapterWikipedia && e.Adapter != AdapterTermContext) {
		return fmt.Errorf("execution.languages, translate, max_topics, max_news, num, min_results, per_domain, max_results, max_concurrency and cache_ttl are only allowed for adapter %s", AdapterNewsSearch)
	}
	if e.Adapter == AdapterFactCheck {
		if err := a.checkFactCheck(); err != nil {
			return err
		}
		if e.Timeout == 0 {
			e.Timeout = 15 * time.Second
		}
	} else if e.PageSize != nil || e.MaxAgeDays != nil || (e.MinCheckWorthiness != nil && e.Adapter != AdapterWikipedia) {
		return fmt.Errorf("execution.min_check_worthiness, page_size and max_age_days are only allowed for adapter %s", AdapterFactCheck)
	}
	if e.MaxClaims != nil && !e.TopicExtraction() && e.Adapter != AdapterFactCheck && e.Adapter != AdapterWikipedia {
		return fmt.Errorf("execution.max_claims is only allowed for adapter %s and execution.task %s", AdapterFactCheck, TaskTopicExtraction)
	}
	if e.Task != "" && e.Adapter != AdapterLLM {
		return fmt.Errorf("execution.task is only allowed for adapter %s", AdapterLLM)
	}
	if e.Method == "" {
		e.Method = "POST"
	}
	if e.Timeout == 0 {
		e.Timeout = 30 * time.Second
		if a.Trigger == TriggerAfterAll && e.Adapter != AdapterLLM { // synthesis is slower than a single analysis
			e.Timeout = 60 * time.Second
		}
	}
	if e.PollInterval == 0 {
		e.PollInterval = time.Second
	}
	if e.Timeout < 0 || e.PollInterval < 0 {
		return fmt.Errorf("timeout and poll_interval must be positive")
	}
	if e.Spans != nil && e.Spans.Timeout >= e.Timeout {
		return fmt.Errorf("execution.spans.timeout must be shorter than execution.timeout (%s)", e.Timeout)
	}
	switch e.Adapter {
	case AdapterHTTPSync, AdapterHTTPAsync, AdapterPropagandaDetection, AdapterBotDetection, AdapterMistralTranslation:
		if e.Endpoint == "" {
			return fmt.Errorf("execution.endpoint is required for adapter %s", e.Adapter)
		}
	case AdapterNewsSearch: // base_url, not endpoint
	case AdapterTechniqueClassifier: // endpoints are in the short and long blocks
	case AdapterSourceInfo: // no endpoint; the table file is checked below
	case AdapterLLMTechniques: // base_url; checked in checkLLMTechniques
	case AdapterWikipedia: // base_url_template; checked in checkWikipedia
	case AdapterTermContext: // base_url and base_url_template; checked in checkTermContext
	case AdapterFactCheck:
		if e.BaseURL == "" {
			e.BaseURL = DefaultFactCheckBaseURL
		}
	case AdapterCustom:
		if !opt.HandlerExists(e.Handler) {
			return fmt.Errorf("unknown custom handler %q", e.Handler)
		}
	case AdapterLLM:
		switch {
		case e.Task != "" && e.Task != TaskFinalSynthesis && e.Task != TaskTopicExtraction:
			return fmt.Errorf("execution.task must be %s or %s", TaskFinalSynthesis, TaskTopicExtraction)
		case e.Task == TaskTopicExtraction && a.Trigger != TriggerOnDeps:
			return fmt.Errorf("execution.task %s is not allowed on %s analyzers", e.Task, a.Trigger)
		case e.Task != TaskTopicExtraction && a.Trigger != TriggerAfterAll:
			return fmt.Errorf("execution.adapter llm is only allowed on %s analyzers (or on analyzers with execution.task: %s)", TriggerAfterAll, TaskTopicExtraction)
		}
		switch {
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
		if e.TopicExtraction() {
			switch {
			case len(a.Input.Bindings) > 0:
				return fmt.Errorf("input.bindings are not allowed for execution.task %s (it analyzes the job input)", e.Task)
			case e.MaxInputChars != nil && *e.MaxInputChars <= 0:
				return fmt.Errorf("execution.max_input_chars must be a positive integer")
			case e.MaxClaims != nil && *e.MaxClaims < 1:
				return fmt.Errorf("execution.max_claims must be a positive integer")
			}
		}
		if e.PromptFile == "" {
			e.PromptFile = DefaultPromptFile
			if e.TopicExtraction() {
				e.PromptFile = DefaultExtractorPrompt
			}
		}
		if e.MaxTokens == 0 {
			e.MaxTokens = 600
			if e.TopicExtraction() {
				e.MaxTokens = defaultExtractorMaxToken
			}
		}
		if e.ResponseFormat == "" {
			e.ResponseFormat = FormatJSONSchema
		}
		if e.ExtraBody == nil {
			e.ExtraBody = map[string]any{}
		}
	default:
		return fmt.Errorf("unknown execution.adapter %q", e.Adapter)
	}
	if a.Enabled {
		var err error
		if e.Endpoint, err = expand(e.Endpoint, opt.Getenv); err != nil {
			return fmt.Errorf("execution.endpoint: %w", err)
		}
		if e.Adapter == AdapterMistralTranslation { // llm expands these once in loadLLM
			if e.APIKey, err = expand(e.APIKey, opt.Getenv); err != nil {
				return fmt.Errorf("execution.api_key: %w", err)
			}
			if e.Model, err = expand(e.Model, opt.Getenv); err != nil {
				return fmt.Errorf("execution.model: %w", err)
			}
		}
		if e.Adapter == AdapterNewsSearch {
			if err := e.expandNewsSearch(opt.Getenv); err != nil {
				return err
			}
		}
		if e.Adapter == AdapterTechniqueClassifier {
			if err := e.expandTechnique(opt.Getenv); err != nil {
				return err
			}
		}
		if e.Adapter == AdapterWikipedia || e.Adapter == AdapterTermContext {
			if err := e.expandWikipedia(opt.Getenv); err != nil {
				return err
			}
		}
		if e.Adapter == AdapterSourceInfo {
			if e.Outlets, err = opt.outletTable(e.OutletsFile); err != nil {
				return fmt.Errorf("execution.outlets_file: %w", err)
			}
		}
		hdr := make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			if hdr[k], err = expand(v, opt.Getenv); err != nil {
				return fmt.Errorf("execution.headers.%s: %w", k, err)
			}
		}
		e.Headers = hdr
		if e.Adapter == AdapterLLM || e.Adapter == AdapterLLMTechniques || e.Adapter == AdapterTermContext {
			if err := e.loadLLM(opt.Getenv, "execution."); err != nil {
				return err
			}
		}
		if e.Adapter == AdapterFactCheck {
			if e.BaseURL, err = expand(e.BaseURL, opt.Getenv); err != nil {
				return fmt.Errorf("execution.base_url: %w", err)
			}
			if e.APIKey, err = expand(e.APIKey, opt.Getenv); err != nil {
				return fmt.Errorf("execution.api_key: %w", err)
			}
			if strings.TrimSpace(e.APIKey) == "" {
				return fmt.Errorf("execution.api_key is empty; it is required for an enabled %s analyzer", e.Adapter)
			}
			if strings.TrimSpace(e.BaseURL) == "" {
				return fmt.Errorf("execution.base_url is empty")
			}
		}
	}
	if a.LLM != nil {
		switch {
		case a.Trigger == TriggerAfterAll:
			return fmt.Errorf("llm block is not allowed on %s analyzers", TriggerAfterAll)
		case a.LLM.Include && strings.TrimSpace(a.LLM.Description) == "":
			return fmt.Errorf("llm.description is required when llm.include is true")
		}
	}
	for name, src := range a.Input.Bindings {
		if err := a.checkBinding(src); err != nil {
			return fmt.Errorf("input.bindings.%s: %w", name, err)
		}
	}
	if a.Output.Schema != nil {
		s, err := compileSchema(a.Output.Schema)
		if err != nil {
			return fmt.Errorf("output.schema: %w", err)
		}
		a.schema = s
	}
	return nil
}

// loadLLM expands the llm settings of an enabled analyzer, checks the required
// ones and reads the prompt file. Errors name the field (after prefix), never a value.
func (e *Execution) loadLLM(getenv func(string) (string, bool), prefix string) error {
	who := "llm finalizer"
	if e.TopicExtraction() {
		who = "topic_extraction analyzer"
	} else if e.Adapter == AdapterTechniqueClassifier {
		who = "technique_classifier with spans"
	} else if e.Adapter == AdapterLLMTechniques {
		who = "llm_techniques analyzer"
	} else if e.Adapter == AdapterTermContext {
		who = "term_context analyzer"
	}
	for _, f := range []struct {
		name     string
		v        *string
		required bool
	}{
		{"base_url", &e.BaseURL, true}, {"api_key", &e.APIKey, true}, {"model", &e.Model, true},
		{"prompt_file", &e.PromptFile, e.Adapter != AdapterTermContext}, {"response_format", &e.ResponseFormat, false},
		{"retry_model", &e.RetryModel, false},
	} {
		var err error
		if *f.v, err = expand(*f.v, getenv); err != nil {
			return fmt.Errorf("%s%s: %w", prefix, f.name, err)
		}
		if f.required && strings.TrimSpace(*f.v) == "" {
			return fmt.Errorf("%s%s is empty; it is required for an enabled %s", prefix, f.name, who)
		}
	}
	switch e.ResponseFormat {
	case FormatJSONSchema, FormatJSONObject, FormatNone:
	default:
		return fmt.Errorf("%sresponse_format must be %s, %s or %s", prefix, FormatJSONSchema, FormatJSONObject, FormatNone)
	}
	body, err := expandAny(e.ExtraBody, getenv)
	if err != nil {
		return fmt.Errorf("%sextra_body: %w", prefix, err)
	}
	e.ExtraBody = body.(map[string]any)
	if e.Adapter == AdapterTermContext { // its prompts are part of the backend
		return nil
	}
	b, err := os.ReadFile(e.PromptFile)
	if err != nil {
		return fmt.Errorf("%sprompt_file cannot be read: %w", prefix, err)
	}
	e.Prompt = string(b)
	return nil
}

// expandAny expands ${VAR} in every string inside v (YAML-decoded values).
func expandAny(v any, getenv func(string) (string, bool)) (any, error) {
	var err error
	switch t := v.(type) {
	case string:
		return expand(t, getenv)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if out[k], err = expandAny(x, getenv); err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			if out[i], err = expandAny(x, getenv); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return v, nil
}

// expand replaces ${VAR}; the error names the variable, never any value.
func expand(s string, getenv func(string) (string, bool)) (string, error) {
	var missing string
	out := varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		v, ok := getenv(name)
		if !ok && missing == "" {
			missing = name
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s is not set", missing)
	}
	return out, nil
}

func (a *Analyzer) checkBinding(src string) error {
	switch {
	case src == "$input", src == "$input.source", src == "$job.id":
		return nil
	case src == "$job.analyzers", src == "$job.coverage", src == "$job.source_language":
		if a.Trigger != TriggerAfterAll {
			return fmt.Errorf("%s is only allowed on %s analyzers", src, TriggerAfterAll)
		}
		return nil
	case strings.HasPrefix(src, "$analyzers."):
		id := strings.TrimPrefix(src, "$analyzers.")
		for _, d := range a.DependsOn {
			if d == id {
				return nil
			}
		}
		return fmt.Errorf("%s: %q is not in depends_on", src, id)
	}
	return fmt.Errorf("unknown binding source %q", src)
}

func compileSchema(m map[string]any) (*jsonschema.Schema, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("mem://schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("mem://schema.json")
}

// validateGraph checks cross-analyzer rules.
func (r *Registry) validateGraph() error {
	finalizers := 0
	for _, a := range r.all {
		if a.Trigger == TriggerAfterAll {
			if a.Enabled {
				finalizers++
				if finalizers > 1 {
					return a.errf("more than one enabled %s analyzer", TriggerAfterAll)
				}
			}
			if len(a.DependsOn) > 0 {
				return a.errf("%s analyzers must not use depends_on", TriggerAfterAll)
			}
		}
		for _, d := range a.DependsOn {
			dep := r.byID[d]
			switch {
			case dep == nil:
				return a.errf("depends_on unknown analyzer %q", d)
			case dep.Trigger == TriggerAfterAll:
				return a.errf("depends_on %q, which is an %s analyzer", d, TriggerAfterAll)
			}
		}
	}
	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var visit func(a *Analyzer) error
	visit = func(a *Analyzer) error {
		state[a.ID] = visiting
		for _, d := range a.DependsOn {
			switch state[d] {
			case visiting:
				return a.errf("dependency cycle through %q", d)
			case 0:
				if err := visit(r.byID[d]); err != nil {
					return err
				}
			}
		}
		state[a.ID] = done
		return nil
	}
	for _, a := range r.all {
		if state[a.ID] == 0 {
			if err := visit(a); err != nil {
				return err
			}
		}
	}
	return nil
}

var langTokenRe = regexp.MustCompile(`^[a-z]{2}$`)

// hasNewsOnlyKeys: keys only news_search accepts; max_results, max_concurrency and cache_ttl are shared with fact_check.
func (e *Execution) hasNewsOnlyKeys() bool {
	return e.Languages != nil || e.Translate != nil || e.MaxTopics != nil || e.MaxNews != nil || e.Num != nil ||
		e.MinResults != nil || e.PerDomain != nil
}

func (e *Execution) hasSharedKeys() bool {
	return e.MaxResults != nil || e.MaxConcurrency != nil || e.CacheTTL != nil
}

// normalizeNewsSearch validates the news_search keys, bindings and dependencies and applies defaults.
func (a *Analyzer) normalizeNewsSearch() error {
	e := &a.Execution
	switch {
	case e.Endpoint != "" || e.Method != "" || e.Handler != "" || e.PollInterval != 0:
		return fmt.Errorf("execution.endpoint, execution.method, execution.handler and execution.poll_interval are not allowed for adapter %s (use base_url)", e.Adapter)
	case strings.TrimSpace(e.BaseURL) == "":
		return fmt.Errorf("execution.base_url is required for adapter %s", e.Adapter)
	case strings.TrimSpace(e.APIKey) == "":
		return fmt.Errorf("execution.api_key is required for adapter %s", e.Adapter)
	}
	hasDep := false
	for _, d := range a.DependsOn {
		hasDep = hasDep || d == "topic_extraction"
	}
	if !hasDep {
		return fmt.Errorf("depends_on must include topic_extraction for adapter %s", e.Adapter)
	}
	for name := range a.Input.Bindings {
		if name != "topics" && name != "translation" {
			return fmt.Errorf("input.bindings.%s is not allowed for adapter %s (only topics and translation)", name, e.Adapter)
		}
	}
	if a.Input.Bindings["topics"] != "$analyzers.topic_extraction" {
		return fmt.Errorf("input.bindings.topics must be $analyzers.topic_extraction")
	}
	if t, ok := a.Input.Bindings["translation"]; ok && !strings.HasPrefix(t, "$analyzers.") {
		return fmt.Errorf("input.bindings.translation must be $analyzers.<id>")
	}
	n := &e.News
	langs := e.Languages
	if langs == nil {
		langs = []string{"source", "en"}
	}
	needsKey := false
	for _, l := range langs {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "source" && !langTokenRe.MatchString(l) {
			return fmt.Errorf("execution.languages: unknown language %q (use source or an ISO 639-1 code)", l)
		}
		needsKey = needsKey || l != "source"
		n.Languages = append(n.Languages, l)
	}
	if len(n.Languages) == 0 {
		return fmt.Errorf("execution.languages must not be empty")
	}
	if e.Translate != nil {
		n.Translate = *e.Translate
	}
	if needsKey && strings.TrimSpace(n.Translate.APIKey) == "" {
		return fmt.Errorf("execution.translate.api_key is required when a language other than source is configured")
	}
	if n.Translate.Model == "" {
		n.Translate.Model = DefaultMistralModel
	}
	if n.Translate.Endpoint == "" {
		n.Translate.Endpoint = DefaultMistralEndpoint
	}
	for _, f := range []struct {
		name string
		in   *int
		out  *int
		def  int
	}{
		{"max_topics", e.MaxTopics, &n.MaxTopics, 3}, {"max_news", e.MaxNews, &n.MaxNews, 2}, {"num", e.Num, &n.Num, 8},
		{"min_results", e.MinResults, &n.MinResults, 2}, {"per_domain", e.PerDomain, &n.PerDomain, 2},
		{"max_results", e.MaxResults, &n.MaxResults, 12}, {"max_concurrency", e.MaxConcurrency, &n.MaxConcurrency, 4},
	} {
		if *f.out = f.def; f.in != nil {
			if *f.in <= 0 {
				return fmt.Errorf("execution.%s must be a positive integer", f.name)
			}
			*f.out = *f.in
		}
	}
	if n.CacheTTL = 6 * time.Hour; e.CacheTTL != nil {
		if *e.CacheTTL <= 0 {
			return fmt.Errorf("execution.cache_ttl must be a positive duration")
		}
		n.CacheTTL = *e.CacheTTL
	}
	if e.Timeout == 0 {
		e.Timeout = 20 * time.Second
	}
	return nil
}

// expandNewsSearch expands ${VAR} in the news_search strings of an enabled analyzer and re-checks the keys.
func (e *Execution) expandNewsSearch(getenv func(string) (string, bool)) error {
	n := &e.News
	for _, f := range []struct {
		name     string
		v        *string
		required bool
	}{
		{"base_url", &e.BaseURL, true}, {"api_key", &e.APIKey, true},
		{"translate.api_key", &n.Translate.APIKey, false}, {"translate.model", &n.Translate.Model, false},
		{"translate.endpoint", &n.Translate.Endpoint, false},
	} {
		var err error
		if *f.v, err = expand(*f.v, getenv); err != nil {
			return fmt.Errorf("execution.%s: %w", f.name, err)
		}
		if f.required && strings.TrimSpace(*f.v) == "" {
			return fmt.Errorf("execution.%s is empty; it is required for an enabled %s analyzer", f.name, e.Adapter)
		}
	}
	for _, l := range n.Languages {
		if l != "source" && strings.TrimSpace(n.Translate.APIKey) == "" {
			return fmt.Errorf("execution.translate.api_key is empty; it is required when a language other than source is configured")
		}
	}
	e.BaseURL = strings.TrimRight(e.BaseURL, "/")
	return nil
}
