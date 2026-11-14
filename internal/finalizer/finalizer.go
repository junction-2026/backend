// Package finalizer is the runtime behind the engine's Finalizer hook: it
// calls the configured finalizer (an http service or an LLM) once all
// analyzers settled and turns its answer into a validated FinalAssessment.
// Nothing here computes or adjusts a verdict or confidence.
//
// The engine owns exactly-once (atomic pending->running transition under the
// per-job lock), the timeout (derived from the job context) and cancellation;
// Run is only invoked once per job.
package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// InsufficientText is the user_text of the orchestrator's short-circuit result.
const InsufficientText = "No analyzer produced a usable result, so no verdict can be given."

// Runtime implements engine.Finalizer.
type Runtime struct {
	Exec engine.Executor
	Reg  *registry.Registry // llm.include/description of the analyzers
	Log  *slog.Logger       // nil: slog.Default()
}

// New returns the runtime to put in engine.Config.Finalizer, or nil (a true
// nil interface, so the engine reports skipped/not_configured) when the
// registry has no enabled after_all_analyzers_settled analyzer.
func New(reg *registry.Registry, exec engine.Executor, log *slog.Logger) engine.Finalizer {
	fd := reg.Finalizer()
	if fd == nil {
		return nil
	}
	r := &Runtime{Exec: exec, Reg: reg, Log: log}
	if fd.Execution.Adapter == registry.AdapterLLM && !anyIncluded(reg) {
		r.log().Warn("llm finalizer has no enabled analyzer with llm.include: true; every job will finalize as uncertain",
			"analyzer_id", fd.ID)
	}
	return r
}

func anyIncluded(reg *registry.Registry) bool {
	for _, a := range reg.All() {
		if a.Enabled && a.LLM != nil && a.LLM.Include {
			return true
		}
	}
	return false
}

func (r *Runtime) log() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

// Run implements engine.Finalizer.
func (r *Runtime) Run(ctx context.Context, in engine.FinalizationInput) (json.RawMessage, error) {
	raw, err := r.run(ctx, in)
	if err != nil {
		// Only the job id and the error code: responses and URLs may carry data or credentials.
		code := job.CodeAdapterError
		var re *job.RunError
		if errors.As(err, &re) {
			code = re.Code
		}
		r.log().Error("finalization failed", "job_id", in.Job.ID, "code", code)
	}
	return raw, err
}

func (r *Runtime) run(ctx context.Context, in engine.FinalizationInput) (json.RawMessage, error) {
	def := in.Analyzer
	if def.Execution.Adapter == registry.AdapterLLM {
		entries, ok := r.llmEntries(&in.Job)
		if !ok {
			return insufficientEvidence()
		}
		var a job.FinalAssessment
		reply, err := adapter.LLM(ctx, def, adapter.LLMUserMessage(def, in.Job.Input.NormalizedContent, entries),
			func(raw json.RawMessage) (err error) {
				if a, err = normalize(raw); err == nil {
					err = def.ValidateOutput(raw) // configured schema judges the finalizer's own answer
				}
				return err
			})
		if err != nil {
			return nil, err
		}
		a.Usage, a.Attempts, a.RetryReason = reply.Usage, &reply.Attempts, reply.RetryReason
		return json.Marshal(a)
	}
	if in.Job.Coverage.Succeeded == 0 {
		return insufficientEvidence()
	}
	raw, err := r.Exec.Execute(ctx, def, adapter.Request{
		JobID: in.Job.ID, AnalyzerID: def.ID, Inputs: adapter.Inputs(def, &in.Job),
	})
	if err != nil {
		return nil, err
	}
	a, err := normalize(raw)
	if err == nil {
		err = def.ValidateOutput(raw)
	}
	if err != nil {
		return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: err.Error()}
	}
	return json.Marshal(a)
}

// llmEntries returns one compact JSON entry per AnalyzerRun opted in to LLM
// input (job order, which is registry id order) and whether any succeeded.
func (r *Runtime) llmEntries(j *job.Job) (entries []json.RawMessage, anySucceeded bool) {
	type entry struct {
		ID           string          `json:"id"`
		Description  string          `json:"description"`
		Status       string          `json:"status"`
		Reason       *string         `json:"reason,omitempty"`
		Availability *string         `json:"availability"`
		Note         *string         `json:"note,omitempty"`
		Result       json.RawMessage `json:"result,omitempty"`
	}
	for _, run := range j.Analyzers {
		def := r.Reg.Get(run.ID)
		if def == nil || def.LLM == nil || !def.LLM.Include {
			continue
		}
		e := entry{ID: run.ID, Description: def.LLM.Description, Status: run.Status}
		e.Availability, e.Note = job.Availability(&run, j.Analyzers)
		switch {
		case run.Status == job.RunSucceeded:
			e.Result, anySucceeded = run.Result, true
		case run.Error != nil:
			e.Reason = &run.Error.Code
		default:
			e.Reason = run.Reason
		}
		b, _ := json.Marshal(e) // compact; RawMessage results are valid JSON
		entries = append(entries, b)
	}
	return entries, anySucceeded
}

// insufficientEvidence is the orchestrator-produced assessment used when no
// analyzer the finalizer would receive succeeded; the finalizer is not called.
func insufficientEvidence() (json.RawMessage, error) {
	return json.Marshal(job.FinalAssessment{Verdict: "uncertain", UserText: InsufficientText, ProducedBy: "orchestrator"})
}

var verdicts = map[string]bool{"detected": true, "not_detected": true, "uncertain": true}

// normalize runs the built-in FinalAssessment checks in a fixed order (the
// error names the first failing member) and returns the stored object:
// verdict, confidence (absent -> null) and user_text unchanged, produced_by
// finalizer; every other member is dropped (usage, attempts and retry_reason
// are set by the caller, never from the output).
func normalize(raw json.RawMessage) (job.FinalAssessment, error) {
	var m map[string]json.RawMessage
	a := job.FinalAssessment{ProducedBy: "finalizer"}
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return a, errors.New("finalizer output is not a JSON object")
	}
	if json.Unmarshal(m["verdict"], &a.Verdict) != nil || !verdicts[a.Verdict] {
		return a, errors.New(`finalizer output member "verdict" is missing or not detected, not_detected or uncertain`)
	}
	// null unmarshals into a string without error, hence isAbsent
	if isAbsent(m["user_text"]) || json.Unmarshal(m["user_text"], &a.UserText) != nil || strings.TrimSpace(a.UserText) == "" {
		return a, errors.New(`finalizer output member "user_text" is missing, not a string or empty`)
	}
	if !isAbsent(m["confidence"]) {
		if json.Unmarshal(m["confidence"], &a.Confidence) != nil || *a.Confidence < 0 || *a.Confidence > 1 {
			return a, errors.New(`finalizer output member "confidence" must be null or a number between 0 and 1`)
		}
	}
	return a, nil
}

func isAbsent(v json.RawMessage) bool { return len(v) == 0 || string(v) == "null" }
