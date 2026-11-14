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
	"github.com/junction-2026/backend/internal/outlets"
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
	// SetOutlets sets the outlet table the map values of jobs that become done afterwards (and the backfill) use.
	SetOutlets(t *outlets.Table)
	// BackfillMap determines the map values of persisted done jobs that have none yet (jobs stored before the map).
	BackfillMap() error
	// MapCountries returns the per-country aggregates of the map analyses, ordered by country code.
	MapCountries() ([]MapCountry, error)
	// MapAnalyses returns up to limit (0: all) map analyses of the country (upper case) newest first (created_at,
	// ties by id), starting after cursor (nil: from the newest).
	MapAnalyses(code string, after *Cursor, limit int) ([]MapItem, error)
	// Close releases the store.
	Close() error
}

// Memory is an in-memory Store without history: jobs are lost on restart.
type Memory struct {
	mu      sync.RWMutex
	jobs    map[string]*job.Record
	outlets *outlets.Table
	vals    map[string]memMap // map values of the done jobs saved so far, fixed at the first save of the done job
}

type memMap struct {
	key mapKey
	MapValues
}

func NewMemory() *Memory { return &Memory{jobs: map[string]*job.Record{}, vals: map[string]memMap{}} }

func (m *Memory) SetOutlets(t *outlets.Table) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outlets = t
}

func (m *Memory) table() *outlets.Table {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.outlets
}

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
			delete(m.vals, id)
			ids = append(ids, id)
		}
	}
	return ids
}

// Save only fixes the map values of a job that is done; the Memory store keeps no history.
func (m *Memory) Save(j *job.Record) error {
	if !j.Done {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.vals[j.ID]; !ok {
		m.vals[j.ID] = memMap{mapKey{j.CreatedAt, j.ID}, DeriveMap(&j.Job, m.outlets)}
	}
	return nil
}
func (m *Memory) BackfillMap() error                     { return nil }
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

func (m *Memory) MapCountries() ([]MapCountry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a := newMapAgg()
	for _, v := range m.vals {
		if v.Eligible {
			a.add(v.key, v.MapValues)
		}
	}
	return a.result(), nil
}

func (m *Memory) MapAnalyses(code string, after *Cursor, limit int) ([]MapItem, error) {
	m.mu.RLock()
	var keys []mapKey
	vals := map[string]MapValues{}
	for id, v := range m.vals {
		if v.Eligible && v.CountryCode == code && (after == nil || mapKey{after.CreatedAt, after.ID}.before(v.key)) {
			keys, vals[id] = append(keys, v.key), v.MapValues
		}
	}
	m.mu.RUnlock()
	sort.Slice(keys, func(a, b int) bool { return keys[a].before(keys[b]) })
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]MapItem, 0, len(keys))
	for _, k := range keys {
		if r, ok := m.Get(k.ID); ok {
			r.Lock()
			out = append(out, MapItem{r.Clone(), vals[k.ID]})
			r.Unlock()
		}
	}
	return out, nil
}
