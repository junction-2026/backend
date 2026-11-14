// Package store keeps job state. State transitions of one job are serialized
// by the job's own mutex (job.Job.Lock), not by the store.
package store

import (
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/job"
)

// Store is the job state repository.
type Store interface {
	Put(j *job.Record)
	Get(id string) (*job.Record, bool)
	// All returns every stored job (unordered).
	All() []*job.Record
	// EvictFinished removes and returns the ids of jobs that finished before cutoff.
	EvictFinished(cutoff time.Time) []string
}

// Memory is an in-memory Store.
type Memory struct {
	mu   sync.RWMutex
	jobs map[string]*job.Record
}

func NewMemory() *Memory { return &Memory{jobs: map[string]*job.Record{}} }

func (m *Memory) Put(j *job.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[j.ID] = j
}

func (m *Memory) Get(id string) (*job.Record, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[id]
	return j, ok
}

func (m *Memory) All() []*job.Record {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*job.Record, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, j)
	}
	return out
}

// EvictFinished never removes a job that is not done.
func (m *Memory) EvictFinished(cutoff time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, j := range m.jobs {
		j.Lock()
		old := j.Done && j.FinishedAt != nil && j.FinishedAt.Before(cutoff)
		j.Unlock()
		if old {
			delete(m.jobs, id)
			ids = append(ids, id)
		}
	}
	return ids
}
