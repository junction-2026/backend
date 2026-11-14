// Package finalizer is the runtime behind the engine's Finalizer hook: it
// calls the configured finalizer service once all analyzers settled and turns
// its answer into a validated FinalAssessment. The scoring methodology lives
// in the external service; nothing here computes or adjusts a score.
//
// The engine owns exactly-once (atomic pending->running transition under the
// per-job lock), the timeout (derived from the job context) and cancellation;
// Run is only invoked once per job.
package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const insufficientExplanation = "No analyzer produced a usable result; no assessment score can be given."

// Runtime implements engine.Finalizer.
type Runtime struct {
	Exec engine.Executor
	Log  *slog.Logger // nil: slog.Default()
}

// New returns the runtime to put in engine.Config.Finalizer, or nil (a true
// nil interface, so the engine reports skipped/not_configured) when the
// registry has no enabled after_all_analyzers_settled analyzer.
func New(reg *registry.Registry, exec engine.Executor, log *slog.Logger) engine.Finalizer {
	if reg.Finalizer() == nil {
		return nil
	}
	return &Runtime{Exec: exec, Log: log}
}

// Run implements engine.Finalizer.
func (r *Runtime) Run(ctx context.Context, in engine.FinalizationInput) (json.RawMessage, error) {
	raw, err := r.run(ctx, in)
	if err != nil {
		// Only the job id and the error code: responses and URLs may carry data or credentials.
		log := r.Log
		if log == nil {
			log = slog.Default()
		}
		code := job.CodeAdapterError
		var re *job.RunError
		if errors.As(err, &re) {
			code = re.Code
		}
		log.Error("finalization failed", "job_id", in.Job.ID, "code", code)
	}
	return raw, err
}

func (r *Runtime) run(ctx context.Context, in engine.FinalizationInput) (json.RawMessage, error) {
	if in.Job.Coverage.Succeeded == 0 {
		return insufficientEvidence(&in.Job)
	}
	raw, err := r.Exec.Execute(ctx, in.Analyzer, adapter.Request{
		JobID: in.Job.ID, AnalyzerID: in.Analyzer.ID, Inputs: adapter.Inputs(in.Analyzer, &in.Job),
	})
	if err != nil {
		return nil, err
	}
	out, err := normalize(raw)
	if err == nil {
		err = in.Analyzer.ValidateOutput(raw) // configured schema judges the service's own answer
	}
	if err != nil {
		return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: err.Error()}
	}
	return out, nil
}

// insufficientEvidence is the orchestrator-produced assessment used when no
// analyzer succeeded; the finalizer service is not called.
func insufficientEvidence(j *job.Job) (json.RawMessage, error) {
	lims := []job.Limitation{{Kind: "missing_evidence", Message: "No analyzer succeeded, so there is no evidence to assess."}}
	for i := range j.Analyzers {
		a := &j.Analyzers[i]
		if a.Status != job.RunFailed {
			continue
		}
		msg := fmt.Sprintf("Analyzer %q failed", a.ID)
		if a.Error != nil {
			msg += " (" + a.Error.Code + ")"
		}
		lims = append(lims, job.Limitation{Kind: "failed_analyzer", Message: msg + ".", AnalyzerIDs: []string{a.ID}})
	}
	return json.Marshal(job.FinalAssessment{
		Verdict: "insufficient_evidence", Categories: []job.FinalCategory{}, Explanation: insufficientExplanation,
		Evidence: []job.EvidenceRef{}, Limitations: lims, ProducedBy: "orchestrator",
	})
}

var verdicts = map[string]bool{"low_risk": true, "medium_risk": true, "high_risk": true, "insufficient_evidence": true}

// normalize runs the built-in FinalAssessment checks in a fixed order (the
// error names the first failing member) and returns the stored object:
// absent overall_score -> null, absent evidence/limitations -> [],
// produced_by -> finalizer, every other member verbatim.
func normalize(raw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("finalizer response is not a JSON object")
	}
	var verdict string
	if json.Unmarshal(m["verdict"], &verdict) != nil || !verdicts[verdict] {
		return nil, errors.New(`finalizer response member "verdict" is missing or not a valid verdict`)
	}
	var cats []json.RawMessage
	if m["categories"] == nil || json.Unmarshal(m["categories"], &cats) != nil || cats == nil {
		return nil, errors.New(`finalizer response member "categories" is missing or not an array`)
	}
	var expl string
	if json.Unmarshal(m["explanation"], &expl) != nil {
		return nil, errors.New(`finalizer response member "explanation" is missing or not a string`)
	}
	hasScore, ok := scoreOK(m["overall_score"])
	if !ok {
		return nil, errors.New(`finalizer response member "overall_score" must be null or a number between 0 and 100`)
	}
	for i, c := range cats {
		var cat map[string]json.RawMessage
		if json.Unmarshal(c, &cat) != nil || cat == nil {
			return nil, fmt.Errorf(`finalizer response member "categories[%d]" is not an object`, i)
		}
		if _, ok := scoreOK(cat["score"]); !ok {
			return nil, fmt.Errorf(`finalizer response member "categories[%d].score" must be null or a number between 0 and 100`, i)
		}
	}
	if verdict == "insufficient_evidence" && hasScore {
		return nil, errors.New(`finalizer response member "overall_score" must be null when verdict is insufficient_evidence`)
	}
	for _, k := range []string{"evidence", "limitations"} {
		if isAbsent(m[k]) {
			m[k] = json.RawMessage("[]")
		} else if m[k][0] != '[' {
			return nil, fmt.Errorf(`finalizer response member %q is not an array`, k)
		}
	}
	if isAbsent(m["overall_score"]) {
		m["overall_score"] = json.RawMessage("null")
	}
	m["produced_by"] = json.RawMessage(`"finalizer"`)
	return json.Marshal(m)
}

func isAbsent(v json.RawMessage) bool { return len(v) == 0 || string(v) == "null" }

// scoreOK reports whether v is absent, null or a number in 0..100.
func scoreOK(v json.RawMessage) (hasNumber bool, ok bool) {
	if isAbsent(v) {
		return false, true
	}
	var f float64
	if json.Unmarshal(v, &f) != nil || f < 0 || f > 100 {
		return false, false
	}
	return true, true
}
