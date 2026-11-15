// Package adapter executes one analyzer: http_sync, http_async (polling),
// propaganda_detection, bot_detection, mistral_translation, technique_classifier, llm_techniques, news_search, wikipedia, term_context and custom (in-process handlers), following analyzer-service-protocol,
// plus llm (OpenAI-compatible chat completions) for the finalizer and the topic_extraction task.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Request is what an analyzer receives.
type Request struct {
	JobID      string
	AnalyzerID string
	Inputs     map[string]any
}

// Executor runs analyzers. Errors it returns are *job.RunError. The caller
// owns the timeout: it passes a context that carries the configured deadline.
type Executor struct {
	Client   *http.Client // defaults to http.DefaultClient (no client timeout; ctx governs)
	Handlers *Handlers

	news      newsCache // news_search: in-memory Serper answers of this process
	factCache factCache // fact_check: in-memory Fact Check answers; the zero value is ready
}

func (x *Executor) Execute(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	switch def.Execution.Adapter {
	case registry.AdapterHTTPSync:
		return x.httpSync(ctx, def, req)
	case registry.AdapterHTTPAsync:
		return x.httpAsync(ctx, def, req)
	case registry.AdapterCustom:
		return x.custom(ctx, def, req)
	case registry.AdapterPropagandaDetection:
		return x.propaganda(ctx, def, req)
	case registry.AdapterBotDetection:
		return x.botDetection(ctx, def, req)
	case registry.AdapterTechniqueClassifier:
		return x.techniqueClassifier(ctx, def, req)
	case registry.AdapterMistralTranslation:
		return x.mistralTranslation(ctx, def, req)
	case registry.AdapterFactCheck:
		return x.factCheck(ctx, def, req)
	case registry.AdapterNewsSearch:
		return x.newsSearch(ctx, def, req)
	case registry.AdapterWikipedia:
		return x.wikipedia(ctx, def, req)
	case registry.AdapterTermContext:
		return x.termContext(ctx, def, req)
	case registry.AdapterSourceInfo:
		return x.sourceInfo(ctx, def, req)
	case registry.AdapterLLMTechniques:
		return x.llmTechniques(ctx, def, req)
	case registry.AdapterLLM:
		if def.Execution.TopicExtraction() {
			return x.topicExtraction(ctx, def, req)
		}
		return nil, adapterErr("the llm adapter runs only as the finalizer")
	}
	return nil, adapterErr("unknown adapter %q", def.Execution.Adapter)
}

func adapterErr(format string, args ...any) *job.RunError {
	return &job.RunError{Code: job.CodeAdapterError, Message: fmt.Sprintf(format, args...)}
}

// ContextError maps a finished context to timeout/cancelled; nil if still live.
func ContextError(ctx context.Context) *job.RunError {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &job.RunError{Code: job.CodeTimeout, Message: "analyzer did not answer within its timeout"}
	case ctx.Err() != nil:
		return &job.RunError{Code: job.CodeCancelled, Message: "analyzer aborted: job cancelled"}
	}
	return nil
}

// Inputs resolves the analyzer's input bindings against the job (caller holds
// the job lock or passes a clone). With no bindings: {"content": $input}.
func Inputs(def *registry.Analyzer, j *job.Job) map[string]any {
	if len(def.Input.Bindings) == 0 {
		return map[string]any{"content": inputObject(j)}
	}
	out := make(map[string]any, len(def.Input.Bindings))
	for name, src := range def.Input.Bindings {
		switch src {
		case "$input":
			out[name] = inputObject(j)
		case "$input.source":
			out[name] = j.Input.Source
		case "$job.id":
			out[name] = j.ID
		case "$job.analyzers":
			out[name] = append([]job.AnalyzerRun(nil), j.Analyzers...)
		case "$job.coverage":
			out[name] = job.ComputeCoverage(j.Analyzers)
		case "$job.source_language":
			out[name] = nil // member present, null when unknown
			if l := JobSourceLanguage(j); l != "" {
				out[name] = l
			}
		default: // $analyzers.<id>, validated by the registry
			id := src[len("$analyzers."):]
			for i := range j.Analyzers {
				if j.Analyzers[i].ID == id {
					out[name] = j.Analyzers[i].Result
				}
			}
		}
	}
	if def.Execution.Adapter == registry.AdapterTechniqueClassifier {
		out[jobContentKey] = j.Input.Content // for the span step only; never sent to the classification models
	}
	if def.Execution.Adapter == registry.AdapterTermContext {
		out[jobContentKey] = j.Input.Content
	}
	if def.Execution.Adapter == registry.AdapterLLMTechniques {
		out[jobContentKey] = j.Input.Content
		if j.Scrape == nil { // a scraped job's title is an account display name, not a headline
			out[jobTitleKey] = j.Input.Source.Title
		}
	}
	return out
}

// JobSourceLanguage is the normalized source_language of the job's succeeded translation run, "" when unknown.
func JobSourceLanguage(j *job.Job) string { return j.SourceLanguage() }

func inputObject(j *job.Job) map[string]any {
	return map[string]any{
		"content":            j.Input.Content,
		"normalized_content": j.Input.NormalizedContent,
		"content_type":       j.Input.ContentType,
		"source":             j.Input.Source,
	}
}
