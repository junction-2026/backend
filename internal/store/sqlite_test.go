package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
)

func open(t *testing.T, path string) *SQLite {
	t.Helper()
	s, err := OpenSQLite(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func save(t *testing.T, s Store, r *job.Record) {
	t.Helper()
	r.Lock()
	defer r.Unlock()
	require.NoError(t, s.Save(r))
}

var t0 = time.Date(2026, 3, 1, 12, 0, 0, 123456789, time.UTC)

func rec(id string, age int, status string) *job.Record {
	at := t0.Add(-time.Duration(age) * time.Second)
	r := &job.Record{Job: job.Job{ID: id, Status: status, CreatedAt: at, Options: job.Options{Finalize: true}, Public: true,
		Finalization: job.Finalization{Status: job.RunPending}}}
	if status == job.StatusCompleted || status == job.StatusFailed || status == job.StatusCancelled {
		r.Done, r.FinishedAt = true, &at
	}
	return r
}

// results of every registered analyzer type, with members the backend does not know and numbers a float64 would change.
var results = map[string]string{
	"techniques":           `{"label":"loaded_language","spans":[{"start":0,"end":4,"future":{"a":[1,2,{"b":null}]}}],"big":12345678901234567890}`,
	"llm_techniques":       `{"findings":[{"technique":"x","confidence":0.1000000000000000055}],"unknown_member":true}`,
	"propaganda_detection": `{"score":0.5,"zz":"ünïcode   <b>&"}`,
	"fact_check":           `{"claims":[],"extra":{"nested":[]}}`,
	"news_search":          `{"articles":[{"title":"t","url":"https://e.x/?a=1&b=2"}],"1e3":1e3}`,
	"topic_extraction":     `{"topics":["a"],"claims":[{"text":"c","check_worthiness":0.9}]}`,
	"source_info":          `{"sources":[],"x":null}`,
	"wikipedia":            `{"claims":[{"passage":"p"}]}`,
	"translation":          `{"source_language":"de","text":"t"}`,
}

const assessment = `{"verdict":"likely_misleading","confidence":0.8,"user_text":"u","produced_by":"llm","usage":{"input_tokens":3},"attempts":1,"retry_reason":null,"wikipedia_quotes":[{"claim":"c","language":"en","quote":"q","title":"T","url":"https://en.wikipedia.org/w/index.php?title=T&oldid=1","revision_id":1,"coverage":"aligned"}],"future_member":{"k":[true]}}`

func fullRecord() *job.Record {
	r := rec("full", 0, job.StatusCompleted)
	r.StartedAt = &t0
	src := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	r.Input = &job.Input{Content: "text", ContentType: "text/plain", Source: job.Source{URL: "https://e.x", PublishedAt: &src, Metadata: json.RawMessage(`{"m":[1]}`)}}
	for _, id := range []string{"fact_check", "llm_techniques", "news_search", "propaganda_detection", "source_info", "techniques", "topic_extraction", "translation", "wikipedia"} {
		r.Analyzers = append(r.Analyzers, job.AnalyzerRun{ID: id, Name: id, Category: "c", DependsOn: []string{}, Status: job.RunSucceeded,
			StartedAt: &t0, FinishedAt: &t0, DurationMS: job.Ptr(int64(5)), Result: json.RawMessage(results[id]), Availability: job.Str(job.AvailRan)})
	}
	r.Analyzers = append(r.Analyzers, job.AnalyzerRun{ID: "dead", Status: job.RunFailed, Error: &job.RunError{Code: "timeout", Message: "m"}})
	r.Coverage = job.ComputeCoverage(r.Analyzers)
	r.Finalization = job.Finalization{Status: job.StatusCompleted, AnalyzerID: job.Str("final"), StartedAt: &t0, FinishedAt: &t0, Result: json.RawMessage(assessment)}
	r.Plan = &job.Plan{Total: 1, Analyzers: []job.PlanEntry{{ID: "techniques", Name: "T", ActivityKey: "techniques", DependsOn: []string{}, Phase: 1}}}
	r.Progress = &job.Progress{Done: 1, Running: []string{}, Total: 1}
	r.Previews = map[string]any{"techniques": map[string]any{"count": 12345678901234567890.0}}
	r.Activities = map[string]job.Activity{}
	r.LastSeq = 42
	return r
}

func TestRoundTripIsLosslessAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "jobs.db") // the directory is created
	s := open(t, path)
	r := fullRecord()
	want, err := json.Marshal(r.Job)
	require.NoError(t, err)
	save(t, s, r)
	require.NoError(t, s.Close())

	s = open(t, path)
	got, ok, err := s.Load("full")
	require.NoError(t, err)
	require.True(t, ok)
	have, err := json.Marshal(got.Job)
	require.NoError(t, err)
	require.JSONEq(t, string(want), string(have))
	for i, a := range r.Analyzers {
		if a.Result != nil {
			require.JSONEq(t, string(a.Result), string(got.Analyzers[i].Result), a.ID+" result is verbatim")
		}
	}
	require.JSONEq(t, assessment, string(got.Finalization.Result), "unknown members of the FinalAssessment are kept")
	require.EqualValues(t, 42, got.LastSeq)
	require.Equal(t, r.Plan, got.Plan)
	require.Equal(t, r.Progress, got.Progress)
	require.Empty(t, got.Activities)
	pw, _ := json.Marshal(r.Previews)
	ph, _ := json.Marshal(got.Previews)
	require.JSONEq(t, string(pw), string(ph))
	require.Equal(t, got.Snapshot().Plan, got.Plan)

	page, err := s.Page("", nil, 0)
	require.NoError(t, err)
	require.Len(t, page, 1)
	pj, _ := json.Marshal(page[0])
	require.JSONEq(t, string(want), string(pj))

	_, ok, err = s.Load("nope")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSaveUpdatesAndUnfinished(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "jobs.db"))
	r := rec("a", 0, job.StatusRunning)
	save(t, s, r)
	save(t, s, rec("b", 1, job.StatusCompleted))
	u, err := s.Unfinished()
	require.NoError(t, err)
	require.Len(t, u, 1)
	require.Equal(t, "a", u[0].ID)
	require.Nil(t, u[0].Plan)
	require.NotNil(t, u[0].Previews)
	require.NotNil(t, u[0].Activities)

	r.Status, r.Done, r.FinishedAt, r.LastSeq = job.StatusCompleted, true, &t0, 7
	save(t, s, r)
	u, _ = s.Unfinished()
	require.Empty(t, u)
	got, _, _ := s.Load("a")
	require.EqualValues(t, 7, got.LastSeq)
	require.Equal(t, job.StatusCompleted, got.Status)
}

func TestMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := open(t, path)
	version := func(s *SQLite) (v int) {
		require.NoError(t, s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v))
		return v
	}
	require.Equal(t, len(migrations), version(s), "an empty file is migrated to the latest version")
	var mode string
	require.NoError(t, s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode)
	save(t, s, rec("a", 0, job.StatusCompleted))
	require.NoError(t, s.Close())

	s = open(t, path) // an already migrated file opens unchanged
	require.Equal(t, len(migrations), version(s))
	_, ok, _ := s.Load("a")
	require.True(t, ok)
	var rows int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&rows))
	require.Equal(t, 1, rows)
	_, err := s.db.Exec(`UPDATE schema_version SET version = ?`, len(migrations)+1)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = OpenSQLite(path)
	require.ErrorContains(t, err, "newer")
}

func TestPrune(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "jobs.db"))
	old, fresh, live := rec("old", 3600, job.StatusCompleted), rec("fresh", 10, job.StatusCompleted), rec("live", 7200, job.StatusRunning)
	for _, r := range []*job.Record{old, fresh, live} {
		save(t, s, r)
	}
	require.NoError(t, s.Prune(t0.Add(-time.Minute)))
	all, _ := s.Page("", nil, 0)
	var ids []string
	for _, j := range all {
		ids = append(ids, j.ID)
	}
	require.Equal(t, []string{"fresh", "live"}, ids, "only done jobs finished before the cutoff are deleted")
}

func TestPageMatchesMemoryOrderFilterAndCursor(t *testing.T) {
	s, m := open(t, filepath.Join(t.TempDir(), "jobs.db")), NewMemory()
	for i, id := range []string{"a", "b", "c", "d", "e", "f"} {
		st := job.StatusRunning
		if i%2 == 0 {
			st = job.StatusCompleted
		}
		r := rec(id, i/2, st) // pairs share created_at: ties by id
		save(t, s, r)
		m.Put(r)
	}
	for _, status := range []string{"", job.StatusRunning, job.StatusCompleted, job.StatusFailed} {
		var after *Cursor
		for {
			a, err := s.Page(status, after, 2)
			require.NoError(t, err)
			b, err := m.Page(status, after, 2)
			require.NoError(t, err)
			ja, _ := json.Marshal(append([]job.Job{}, a...))
			jb, _ := json.Marshal(append([]job.Job{}, b...))
			require.JSONEq(t, string(jb), string(ja), "status %q after %v", status, after)
			if len(a) < 2 {
				break
			}
			after = &Cursor{CreatedAt: a[1].CreatedAt, ID: a[1].ID}
		}
	}
}

func TestPageDecodesOnlyThePage(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "jobs.db"))
	tx, err := s.db.Begin()
	require.NoError(t, err)
	for i := 0; i < 10000; i++ {
		r := rec(fmt.Sprintf("job-%05d", i), 0, job.StatusCompleted)
		r.CreatedAt = t0.Add(-time.Duration(i) * time.Millisecond)
		data, _ := json.Marshal(r.Job)
		_, err := tx.Exec(`INSERT INTO jobs (id, status, done, created_at, finished_at, data, state) VALUES (?, ?, 1, ?, ?, ?, '{}')`,
			r.ID, r.Status, r.CreatedAt.UnixNano(), nanos(r.FinishedAt), string(data))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())

	first, err := s.Page("", nil, 21)
	require.NoError(t, err)
	require.Len(t, first, 21)
	require.EqualValues(t, 21, s.decoded.Load())
	s.decoded.Store(0)
	deep, err := s.Page(job.StatusCompleted, &Cursor{CreatedAt: first[19].CreatedAt, ID: first[19].ID}, 21)
	require.NoError(t, err)
	require.Equal(t, first[20].ID, deep[0].ID)
	require.EqualValues(t, 21, s.decoded.Load())

	for _, q := range []string{
		`EXPLAIN QUERY PLAN SELECT data FROM jobs ORDER BY created_at DESC, id LIMIT 21`,
		`EXPLAIN QUERY PLAN SELECT data FROM jobs WHERE status = 'completed' ORDER BY created_at DESC, id LIMIT 21`,
	} {
		rows, err := s.db.Query(q)
		require.NoError(t, err)
		var plan string
		for rows.Next() {
			var a, b, c int
			var d string
			require.NoError(t, rows.Scan(&a, &b, &c, &d))
			plan += d + ";"
		}
		require.NoError(t, rows.Close())
		require.Contains(t, plan, "USING INDEX", q)
		require.NotContains(t, plan, "TEMP B-TREE", "an index provides the order: "+q)
	}
}

func TestConcurrentSaveLoadPage(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "jobs.db"))
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				r := rec(fmt.Sprintf("w%d-%d", w, i%5), i, job.StatusRunning)
				r.Lock()
				r.LastSeq = int64(i)
				require.NoError(t, s.Save(r))
				r.Unlock()
				_, _, err := s.Load(r.ID)
				require.NoError(t, err)
				_, err = s.Page("", nil, 10)
				require.NoError(t, err)
			}
		}()
	}
	wg.Wait()
}

func TestOpenErrors(t *testing.T) {
	_, err := OpenSQLite(t.TempDir()) // a directory is not a database
	require.Error(t, err)
	var _ *sql.DB = open(t, filepath.Join(t.TempDir(), "x y#?.db")).db // unusual characters in the path
}

// interleaved: public jobs p1..p3 and unlisted u1, u2, newest first u2 p3 u1 p2 p1.
func interleaved(t *testing.T, stores ...Store) {
	t.Helper()
	for i, id := range []string{"p1", "p2", "u1", "p3", "u2"} {
		r := rec(id, 10-i, []string{job.StatusRunning, job.StatusCompleted}[i%2])
		r.Public = id[0] == 'p'
		for _, s := range stores {
			save(t, s, r)
			if m, ok := s.(*Memory); ok {
				m.Put(r)
			}
		}
	}
}

func TestPageExcludesNonPublicBeforePaging(t *testing.T) {
	dir := t.TempDir()
	s := open(t, filepath.Join(dir, "jobs.db"))
	m := NewMemory()
	interleaved(t, s, m)
	for name, st := range map[string]Store{"sqlite": s, "memory": m} {
		for _, status := range []string{"", job.StatusRunning, job.StatusCompleted} {
			var all, want []string
			for _, j := range []string{"p3", "p2", "p1"} {
				r, ok := m.Get(j)
				require.True(t, ok)
				if status == "" || r.Status == status {
					want = append(want, j)
				}
			}
			var after *Cursor
			for {
				page, err := st.Page(status, after, 2)
				require.NoError(t, err)
				for _, j := range page {
					require.True(t, j.Public, name)
					all = append(all, j.ID)
				}
				if len(page) < 2 {
					break
				}
				require.Len(t, page, 2, "%s: a page holds limit public jobs", name)
				after = &Cursor{CreatedAt: page[1].CreatedAt, ID: page[1].ID}
			}
			require.Equal(t, want, all, "%s status %q", name, status)
		}
		first, err := st.Page("", nil, 2)
		require.NoError(t, err)
		require.Equal(t, "p3", first[0].ID)
		require.Equal(t, "p2", first[1].ID)
		rest, err := st.Page("", &Cursor{CreatedAt: first[1].CreatedAt, ID: first[1].ID}, 2)
		require.NoError(t, err)
		require.Len(t, rest, 1, "%s: next_cursor is null when no public job follows", name)
	}
}

func TestUnlistedJobSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := open(t, path)
	interleaved(t, s)
	require.NoError(t, s.Close())
	s = open(t, path)
	r, ok, err := s.Load("u1")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, r.Public)
	page, err := s.Page("", nil, 0)
	require.NoError(t, err)
	require.Len(t, page, 3)
}

func TestMigratesDatabaseWithoutPublicColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := open(t, path)
	save(t, s, rec("old", 1, job.StatusCompleted))
	// put the file back into its schema version 1 state: no column, no index, and a Job stored without `public`
	for _, q := range []string{`DROP INDEX jobs_map`, `ALTER TABLE jobs DROP COLUMN map_eligible`, `ALTER TABLE jobs DROP COLUMN map_country_code`,
		`ALTER TABLE jobs DROP COLUMN map_country`, `ALTER TABLE jobs DROP COLUMN map_outlet`, `ALTER TABLE jobs DROP COLUMN map_techniques`,
		`DROP INDEX jobs_public_created`, `DROP INDEX jobs_public_status_created`, `ALTER TABLE jobs DROP COLUMN public`,
		`UPDATE jobs SET data = json_remove(data, '$.public')`, `UPDATE schema_version SET version = 1`} {
		_, err := s.db.Exec(q)
		require.NoError(t, err, q)
	}
	require.NoError(t, s.Close())

	s = open(t, path)
	page, err := s.Page("", nil, 0)
	require.NoError(t, err)
	require.Len(t, page, 1, "a pre-existing job is listed")
	require.True(t, page[0].Public)
	r, _, _ := s.Load("old")
	require.True(t, r.Public)
	var public int
	require.NoError(t, s.db.QueryRow(`SELECT public FROM jobs WHERE id = 'old'`).Scan(&public))
	require.Equal(t, 1, public)
	require.NoError(t, s.Close())
	open(t, path) // idempotent: a second start changes nothing
}
