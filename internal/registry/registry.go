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
	e := &a.Execution
	if e.Method == "" {
		e.Method = "POST"
	}
	if e.Timeout == 0 {
		e.Timeout = 30 * time.Second
		if a.Trigger == TriggerAfterAll { // synthesis is slower than a single analysis
			e.Timeout = 60 * time.Second
		}
	}
	if e.PollInterval == 0 {
		e.PollInterval = time.Second
	}
	if e.Timeout < 0 || e.PollInterval < 0 {
		return fmt.Errorf("timeout and poll_interval must be positive")
	}
	switch e.Adapter {
	case AdapterHTTPSync, AdapterHTTPAsync:
		if e.Endpoint == "" {
			return fmt.Errorf("execution.endpoint is required for adapter %s", e.Adapter)
		}
	case AdapterCustom:
		if !opt.HandlerExists(e.Handler) {
			return fmt.Errorf("unknown custom handler %q", e.Handler)
		}
	default:
		return fmt.Errorf("unknown execution.adapter %q", e.Adapter)
	}
	if a.Enabled {
		var err error
		if e.Endpoint, err = expand(e.Endpoint, opt.Getenv); err != nil {
			return fmt.Errorf("execution.endpoint: %w", err)
		}
		hdr := make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			if hdr[k], err = expand(v, opt.Getenv); err != nil {
				return fmt.Errorf("execution.headers.%s: %w", k, err)
			}
		}
		e.Headers = hdr
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
	case src == "$job.analyzers", src == "$job.coverage":
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
