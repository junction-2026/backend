package chat

import (
	"math"
	"sync"
	"time"
)

// limiter is the per-job token bucket, the one-in-flight-per-job rule and the global in-flight cap. A request is
// admitted atomically or rejected without consuming anything.
type limiter struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	burst    float64
	maxConc  int
	inFlight int
	jobs     map[string]*bucket
	now      func() time.Time
}

type bucket struct {
	tokens   float64
	last     time.Time
	inFlight bool
}

func newLimiter(perMinute, maxConc int, now func() time.Time) *limiter {
	return &limiter{rate: float64(perMinute) / 60, burst: float64(perMinute), maxConc: maxConc, jobs: map[string]*bucket{}, now: now}
}

const sweepAbove = 1024

func (l *limiter) acquire(jobID string) (release func(), retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.jobs) > sweepAbove { // forget idle jobs whose bucket is full again
		for id, b := range l.jobs {
			if !b.inFlight && b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
				delete(l.jobs, id)
			}
		}
	}
	b := l.jobs[jobID]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.jobs[jobID] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	switch {
	case b.inFlight, l.inFlight >= l.maxConc:
		return nil, 1
	case b.tokens < 1:
		return nil, max(1, int(math.Ceil((1-b.tokens)/l.rate)))
	}
	b.tokens--
	b.inFlight = true
	l.inFlight++
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		b.inFlight = false
		l.inFlight--
	}, 0
}
