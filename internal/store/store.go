// Package store keeps job state. State transitions of one job are serialized
// by the job's own mutex (job.Job.Lock), not by the store.
//
// A Store has two parts: the working set of live records (Put, Get, EvictFinished), which every implementation
// keeps in memory, and the history (Save, Load, Page, Unfinished, Prune), which the Memory store leaves empty and
// the SQLite store keeps on disk.
package store

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
)

// Matches tells whether the lowercased search q ("": any) is a literal substring of the job's id, input.content
// or scrape.url, compared after Unicode lowercase mapping.
func Matches(j *job.Job, q string) bool {
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), q) }
	return q == "" || has(j.ID) || (j.Input != nil && has(j.Input.Content)) || (j.Scrape != nil && has(j.Scrape.URL))
}

// LanguageUnknown as the language filter of Page lists only jobs whose job language is unknown.
const LanguageUnknown = "unknown"

// matchesLanguage tells whether the job language l ("": unknown) passes the filter lang (see Page).
func matchesLanguage(l, lang string) bool {
	return lang == "" || l == lang || (lang == LanguageUnknown && l == "")
}

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
	// status, only public jobs matching the search q (see Matches; "": no search) and with the job language lang
	// (job.Job.SourceLanguage; "": any, LanguageUnknown: none), the limit and the cursor counting those only, starting
	// after cursor (nil: from the newest). The job language is stored when the job is saved, so without q only the
	// jobs it returns are decoded.
	Page(status, q, lang string, after *Cursor, limit int) ([]job.Job, error)
	// Unfinished returns the persisted jobs that are not done, as unshared records.
	Unfinished() ([]*job.Record, error)
	// Prune deletes persisted done jobs that finished before cutoff.
	Prune(cutoff time.Time) error
	// SetOutlets sets the outlet table the map values of jobs that become done afterwards (and the backfill) use.
	SetOutlets(t *outlets.Table)
	// BackfillMap determines the map values of persisted done jobs that have none yet (jobs stored before the map).
	BackfillMap() error
	// MapCountries returns the per-country aggregates of the map analyses, ordered by country code (an analysis is
	// counted in each of its map countries), and the number of distinct map analyses.
	MapCountries() ([]MapCountry, int, error)
	// MapAnalyses returns up to limit (0: all) map analyses whose map countries include the country (upper case)
	// newest first (created_at, ties by id), starting after cursor (nil: from the newest).
	MapAnalyses(code string, after *Cursor, limit int) ([]MapItem, error)
	// CountryBackfill returns up to limit persisted done public jobs with detected techniques whose
	// country_association is missing, null or failed and that had fewer than maxAttempts backfill attempts, fewest
	// attempts first, then newest. The Memory store (no history) returns none.
	CountryBackfill(limit, maxAttempts int) ([]job.Job, error)
	// SetCountryAssociation counts a backfill attempt of the persisted job and, when a is not nil and the job's
	// association is still missing, null or failed, sets it (also on the live record) and determines the job's map
	// countries again with its fixed map outlet and technique set. It reports whether a was set; a job settled
	// meanwhile is never changed.
	SetCountryAssociation(id string, a *job.CountryAssociation) (bool, error)
	// Close releases the store.
	Close() error
}

// Memory is an in-memory Store without history: jobs are lost on restart.
type Memory struct {
	mu      sync.RWMutex
	jobs    map[string]*job.Record
	outlets *outlets.Table
	vals    map[string]memMap // map values of the done jobs saved so far, fixed at the first save of the done job
	langs   sync.Map          // id → job language as of the job's last save ("": unknown); not under mu, which Save need not take
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
			m.langs.Delete(id)
			ids = append(ids, id)
		}
	}
	return ids
}

// Save only records the job language and fixes the map values of a job that is done; the Memory store keeps no history.
func (m *Memory) Save(j *job.Record) error {
	m.langs.Store(j.ID, j.SourceLanguage())
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
func (m *Memory) BackfillMap() error                          { return nil }
func (m *Memory) CountryBackfill(int, int) ([]job.Job, error) { return nil, nil }
func (m *Memory) SetCountryAssociation(string, *job.CountryAssociation) (bool, error) {
	return false, nil
}
func (m *Memory) Load(string) (*job.Record, bool, error) { return nil, false, nil }
func (m *Memory) Unfinished() ([]*job.Record, error)     { return nil, nil }
func (m *Memory) Prune(time.Time) error                  { return nil }
func (m *Memory) Close() error                           { return nil }

// Page lists the working set; the language filter uses the job language recorded by the last Save.
func (m *Memory) Page(status, q, lang string, after *Cursor, limit int) ([]job.Job, error) {
	var out []job.Job
	q = strings.ToLower(q)
	for _, j := range m.All() {
		j.Lock()
		l, _ := m.langs.Load(j.ID)
		code, _ := l.(string) // never saved: unknown
		if j.Public && (status == "" || j.Status == status) && matchesLanguage(code, lang) && Matches(&j.Job, q) {
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

func (m *Memory) MapCountries() ([]MapCountry, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, n := newMapAgg(), 0
	for _, v := range m.vals {
		if v.Eligible {
			a.add(v.key, v.MapValues)
			n++
		}
	}
	return a.result(), n, nil
}

func (m *Memory) MapAnalyses(code string, after *Cursor, limit int) ([]MapItem, error) {
	m.mu.RLock()
	var keys []mapKey
	vals := map[string]MapValues{}
	for id, v := range m.vals {
		if _, in := v.Country(code); v.Eligible && in && (after == nil || mapKey{after.CreatedAt, after.ID}.before(v.key)) {
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
