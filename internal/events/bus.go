// Package events is the per-job ordered event bus: seq per job starting at 1
// without gaps, snapshot-on-subscribe and slow-consumer detection.
package events

import (
	"errors"
	"sync"
	"time"
)

// ErrSlowConsumer is Subscription.Err() after the buffer overflowed.
var ErrSlowConsumer = errors.New("slow consumer")

// Event is the event envelope.
type Event struct {
	Type      string    `json:"type"`
	JobID     string    `json:"job_id"`
	Seq       int64     `json:"seq"`
	EmittedAt time.Time `json:"emitted_at"`
	Data      any       `json:"data"`
}

// Subscription delivers a snapshot followed by every later event in order.
// The channel is closed when the job stream ends (Err() == nil) or the
// consumer fell behind (Err() == ErrSlowConsumer).
type Subscription struct {
	ch  chan Event
	err error
	s   *stream
	b   *Bus
}

func (s *Subscription) Events() <-chan Event { return s.ch }

// Err is valid after Events() is closed.
func (s *Subscription) Err() error {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.err
}

// Close unsubscribes; safe to call more than once.
func (s *Subscription) Close() {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.s.drop(s, nil)
}

type stream struct {
	seq    int64
	closed bool
	subs   map[*Subscription]struct{}
}

func (st *stream) drop(sub *Subscription, err error) {
	if _, ok := st.subs[sub]; ok {
		delete(st.subs, sub)
		sub.err = err
		close(sub.ch)
	}
}

// Bus holds one stream per job.
type Bus struct {
	mu     sync.Mutex
	buffer int
	now    func() time.Time
	st     map[string]*stream
}

// New returns a bus whose subscribers buffer at most buffer events (min 2).
func New(buffer int) *Bus {
	return &Bus{buffer: max(buffer, 2), now: time.Now, st: map[string]*stream{}}
}

func (b *Bus) stream(id string) *stream {
	st := b.st[id]
	if st == nil {
		st = &stream{subs: map[*Subscription]struct{}{}}
		b.st[id] = st
	}
	return st
}

// Publish assigns the next seq and delivers the event to all subscribers.
// Callers publish events of one job from one serialized section.
func (b *Bus) Publish(jobID, typ string, data any) Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stream(jobID)
	st.seq++
	ev := Event{Type: typ, JobID: jobID, Seq: st.seq, EmittedAt: b.now().UTC(), Data: data}
	for sub := range st.subs {
		select {
		case sub.ch <- ev:
		default:
			st.drop(sub, ErrSlowConsumer)
		}
	}
	return ev
}

// Subscribe registers a subscriber atomically with taking the snapshot:
// snapshot() runs while no event can be published (it must not call the bus)
// and its result becomes the first event (type job.snapshot, seq = last seq).
// On a closed stream only the snapshot is delivered.
func (b *Bus) Subscribe(jobID string, snapshot func() any) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stream(jobID)
	sub := &Subscription{ch: make(chan Event, b.buffer+1), s: st, b: b}
	sub.ch <- Event{Type: "job.snapshot", JobID: jobID, Seq: st.seq, EmittedAt: b.now().UTC(), Data: snapshot()}
	if st.closed {
		close(sub.ch)
	} else {
		st.subs[sub] = struct{}{}
	}
	return sub
}

// Close ends the stream of a job (after its terminal event): subscribers are
// closed normally and later subscribers only get the snapshot.
func (b *Bus) Close(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.stream(jobID)
	st.closed = true
	for sub := range st.subs {
		st.drop(sub, nil)
	}
}

// Remove forgets a job's stream (eviction).
func (b *Bus) Remove(jobID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.st[jobID]; st != nil {
		for sub := range st.subs {
			st.drop(sub, nil)
		}
		delete(b.st, jobID)
	}
}

// Closed returns a subscription that delivers only a job.snapshot at seq and then ends: the stream of a job that was
// reloaded from persistent history (seq is its last emitted seq). The bus keeps no state for it.
func (b *Bus) Closed(jobID string, seq int64, snapshot any) *Subscription {
	sub := &Subscription{ch: make(chan Event, 1), s: &stream{closed: true}, b: b}
	sub.ch <- Event{Type: "job.snapshot", JobID: jobID, Seq: seq, EmittedAt: b.now().UTC(), Data: snapshot}
	close(sub.ch)
	return sub
}
