package finalizer

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/junction-2026/backend/internal/engine"
)

// A subscription gets job.snapshot plus only later events, so a test must
// subscribe before its job starts. newEngine runs one job at a time and
// create() first queues a gate job that holds the single slot inside the
// finalizer hook; the test job then stays queued (no event emitted) until
// create() has subscribed and opens the gate. No sleeps or retries.

const gateContent = "\x00gate"

var gates sync.Map // gate job id -> chan struct{}, closed to release the slot

func gateCh(id string) chan struct{} {
	c, _ := gates.LoadOrStore(id, make(chan struct{}))
	return c.(chan struct{})
}

// gatedFinalizer blocks gate jobs until released and delegates everything else.
type gatedFinalizer struct{ engine.Finalizer }

func (g gatedFinalizer) Run(ctx context.Context, in engine.FinalizationInput) (json.RawMessage, error) {
	if in.Job.Input.Content != gateContent {
		return g.Finalizer.Run(ctx, in)
	}
	<-gateCh(in.Job.ID) // not ctx-bound: the finalizer timeout must not free the slot early
	return nil, errors.New("gate job")
}
