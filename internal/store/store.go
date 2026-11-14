// Package store keeps job state. State transitions of one job are serialized
// by the job's own mutex (job.Job.Lock), not by the store.
//
// A Store has two parts: the working set of live records (Put, Get, EvictFinished), which every implementation
// keeps in memory, and the history (Save, Load, Page, Unfinished, Prune), which the Memory store leaves empty and
// the SQLite store keeps on disk.
package store

import (
	"sort"
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/job"
)

// Cursor is the position after which a page starts (the last job of the previous page).
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// Store is the job state repository; the engine's only persistence boundary.
type Store interface {
	// Put adds a live record to the working set.
	Put(j *job.Record)
	// Get returns a live record of the working set.
	Get(id string) (*job.Record, bool)
	// EvictFinished removes jobs that finished before cutoff from the working set only and returns their ids.
	EvictFinished(cutoff time.Time) []string

	// Save persists the job. The caller holds j's lock; the store copies what it needs before returning.
	Save(j *job.Record) error
	// Load returns an unshared record of a persisted job, with its event-stream state and LastSeq.
	Load(id string) (*job.Record, bool, error)
	// Page returns up to limit jobs (0: all) newest first (created_at, ties by id), optionally only those with
	// status, only public jobs (the limit and the cursor count those only), starting after cursor (nil: from the newest). Only the jobs it returns are decoded.
	Page(status string, after *Cursor, limit int) ([]job.Job, error)
	// Unfinished returns the persisted jobs that are not done, as unshared records.
	Unfinished() ([]*job.Record, error)
	// Prune deletes persisted done jobs that finished before cutoff.
	Prune(cutoff time.Time) error
	// Close releases the store.
	Close() error
}

// Memory is an in-memory Store without history: jobs are lost on restart.
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

// All returns every stored job (unordered).
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

func (m *Memory) Save(*job.Record) error                 { return nil }
func (m *Memory) Load(string) (*job.Record, bool, error) { return nil, false, nil }
func (m *Memory) Unfinished() ([]*job.Record, error)     { return nil, nil }
func (m *Memory) Prune(time.Time) error                  { return nil }
func (m *Memory) Close() error                           { return nil }

// Page lists the working set.
func (m *Memory) Page(status string, after *Cursor, limit int) ([]job.Job, error) {
	var out []job.Job
	for _, j := range m.All() {
		j.Lock()
		if j.Public && (status == "" || j.Status == status) {
			out = append(out, j.Clone())
		}
		j.Unlock()
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.After(out[b].CreatedAt)
		}
		return out[a].ID < out[b].ID
	})
	if after != nil {
		start := sort.Search(len(out), func(i int) bool {
			return out[i].CreatedAt.Before(after.CreatedAt) || (out[i].CreatedAt.Equal(after.CreatedAt) && out[i].ID > after.ID)
		})
		out = out[start:]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
