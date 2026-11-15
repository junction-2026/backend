package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
)

// translated gives r a translation run with the status and raw result ("" for none).
func translated(r *job.Record, status, result string) *job.Record {
	run := job.AnalyzerRun{ID: "translation", Status: status}
	if result != "" {
		run.Result = json.RawMessage(result)
	}
	r.Analyzers = append(r.Analyzers, run)
	return r
}

// languageJobs: 12 jobs newest first, all containing "needle"; ids name language and status.
func languageJobs() []*job.Record {
	var out []*job.Record
	add := func(id, status, trStatus, result string, public bool) {
		r := translated(rec(id, len(out), status), trStatus, result)
		r.Input, r.Public = &job.Input{Content: "a needle in " + id}, public
		out = append(out, r)
	}
	add("de-completed-1", job.StatusCompleted, job.RunSucceeded, `{"source_language":"de"}`, true)
	add("en-completed", job.StatusCompleted, job.RunSucceeded, `{"source_language":"en"}`, true)
	add("de-failed", job.StatusFailed, job.RunSucceeded, `{"source_language":" DE "}`, true)
	add("fr-failed", job.StatusFailed, job.RunSucceeded, `{"source_language":"fr"}`, true)
	add("unk-null", job.StatusCompleted, job.RunSucceeded, `{"source_language":null}`, true)
	add("unk-word", job.StatusCompleted, job.RunSucceeded, `{"source_language":"german"}`, true)
	add("unk-failed", job.StatusFailed, job.RunFailed, `{"source_language":"de"}`, true)
	add("unk-skipped", job.StatusCompleted, job.RunSkipped, ``, true)
	add("unk-running", job.StatusRunning, job.RunRunning, ``, true)
	add("de-private", job.StatusCompleted, job.RunSucceeded, `{"source_language":"de"}`, false)
	add("de-completed-2", job.StatusCompleted, job.RunSucceeded, `{"source_language":"de"}`, true)
	r := rec("unk-none", len(out), job.StatusCompleted) // no translation run at all
	r.Input = &job.Input{Content: "needle"}
	return append(out, r)
}

// want lists the ids of jobs matching the filters, newest first, by brute force.
func want(jobs []*job.Record, status, q, lang string) []string {
	var ids []string
	for _, r := range jobs {
		l := r.SourceLanguage()
		if r.Public && (status == "" || r.Status == status) && Matches(&r.Job, q) &&
			(lang == "" || l == lang || (lang == LanguageUnknown && l == "")) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func pageIDs(t *testing.T, s Store, status, q, lang string, limit int) []string {
	t.Helper()
	var ids []string
	var after *Cursor
	for {
		p, err := s.Page(status, q, lang, after, limit)
		require.NoError(t, err)
		require.LessOrEqual(t, len(p), limit)
		for _, j := range p {
			ids = append(ids, j.ID)
		}
		if len(p) < limit {
			return ids
		}
		after = &Cursor{CreatedAt: p[len(p)-1].CreatedAt, ID: p[len(p)-1].ID}
	}
}

func TestPageLanguageFilter(t *testing.T) {
	jobs := languageJobs()
	s, m := open(t, filepath.Join(t.TempDir(), "jobs.db")), NewMemory()
	for _, r := range jobs {
		m.Put(r)
		save(t, m, r)
		save(t, s, r)
	}
	require.Equal(t, []string{"de-completed-1", "de-failed", "de-completed-2"}, want(jobs, "", "", "de"))
	for name, st := range map[string]Store{"sqlite": s, "memory": m} {
		for _, status := range []string{"", job.StatusCompleted, job.StatusFailed, job.StatusRunning} {
			for _, q := range []string{"", "needle", "de-", "unk"} {
				for _, lang := range []string{"", "de", "en", "fr", "zz", LanguageUnknown} {
					for _, limit := range []int{1, 2, 100} {
						require.Equal(t, want(jobs, status, q, lang), pageIDs(t, st, status, q, lang, limit),
							"%s status %q q %q language %q limit %d", name, status, q, lang, limit)
					}
				}
			}
		}
	}
	require.Equal(t, []string{"de-failed"}, pageIDs(t, s, job.StatusFailed, "needle", "de", 20), "language with q and status")
	require.Equal(t, []string{"unk-null", "unk-word", "unk-failed", "unk-skipped", "unk-running", "unk-none"},
		pageIDs(t, s, "", "", LanguageUnknown, 20))

	s.decoded.Store(0)
	p, err := s.Page("", "", "de", nil, 2)
	require.NoError(t, err)
	require.Len(t, p, 2)
	require.EqualValues(t, 2, s.decoded.Load(), "the language filter runs in SQL: only the page is decoded")
	var plan string
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT data FROM jobs WHERE public = 1 AND language = ? ORDER BY created_at DESC, id LIMIT 2`, "de")
	require.NoError(t, err)
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		plan += detail + "\n"
	}
	require.NoError(t, rows.Close())
	require.Contains(t, plan, "jobs_public_language_created")
}

func TestStoredLanguageFollowsTranslation(t *testing.T) {
	s, m := open(t, filepath.Join(t.TempDir(), "jobs.db")), NewMemory()
	for name, st := range map[string]Store{"sqlite": s, "memory": m} {
		r := translated(rec("j-"+name, 0, job.StatusRunning), job.RunRunning, "")
		m.Put(r)
		save(t, st, r)
		require.Equal(t, []string{r.ID}, pageIDs(t, st, "", r.ID, LanguageUnknown, 10), name+": unsettled is unknown")
		r.Analyzers[0].Status, r.Analyzers[0].Result = job.RunSucceeded, json.RawMessage(`{"source_language":"RU"}`)
		save(t, st, r)
		require.Equal(t, []string{r.ID}, pageIDs(t, st, "", r.ID, "ru", 10), name+": set when translation succeeds")
		require.Empty(t, pageIDs(t, st, "", r.ID, LanguageUnknown, 10))
		r.Status, r.Done = job.StatusCompleted, true
		save(t, st, r)
		require.Equal(t, []string{r.ID}, pageIDs(t, st, "", r.ID, "ru", 10), name+": unchanged after success")

		ir := translated(rec("i-"+name, 0, job.StatusRunning), job.RunRunning, "")
		m.Put(ir)
		save(t, st, ir)
		ir.Interrupt(t0)
		save(t, st, ir)
		require.Equal(t, job.RunFailed, ir.Analyzers[0].Status)
		require.Equal(t, []string{ir.ID}, pageIDs(t, st, "", ir.ID, LanguageUnknown, 10), name+": interrupted is unknown")
	}
}

// downgrade puts the file back into its schema version 4 state (before the language column).
func downgrade(t *testing.T, s *SQLite) {
	t.Helper()
	for _, q := range []string{`DROP INDEX jobs_public_language_created`, `ALTER TABLE jobs DROP COLUMN language`, `UPDATE schema_version SET version = 4`} {
		_, err := s.db.Exec(q)
		require.NoError(t, err, q)
	}
}

func TestMigrationBackfillsLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	s := open(t, path)
	jobs := languageJobs()
	for _, r := range jobs {
		save(t, s, r)
	}
	before := pageIDs(t, s, "", "", "", 100)
	downgrade(t, s)
	// a row whose stored job does not decode, and more rows than one backfill batch
	_, err := s.db.Exec(`INSERT INTO jobs (id, status, done, created_at, public, data, state) VALUES ('bad', 'completed', 1, 0, 0, '{"analyzers":"x"}', '{}')`)
	require.NoError(t, err)
	_, err = s.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO jobs (id, status, done, created_at, public, data, state)
		SELECT printf('fill%04d', i), 'completed', 1, i, 0, '{"analyzers":[{"id":"translation","status":"succeeded","result":{"source_language":"Ru"}}]}', '{}' FROM n`,
		2*languageBatch+7)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// a failing open rolls the whole migration back
	orig := backfills[4]
	backfills[4] = func(ctx context.Context, tx *sql.Tx) error {
		if err := orig(ctx, tx); err != nil {
			return err
		}
		return errors.New("crash")
	}
	_, err = OpenSQLite(path)
	backfills[4] = orig
	require.ErrorContains(t, err, "crash")

	for i := 0; i < 2; i++ { // the second open changes nothing
		s = open(t, path)
		var v int
		require.NoError(t, s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v))
		require.Equal(t, len(migrations), v)
		langs := map[string]*string{}
		rows, err := s.db.Query(`SELECT id, language FROM jobs WHERE id NOT LIKE 'fill%'`)
		require.NoError(t, err)
		for rows.Next() {
			var id string
			var l *string
			require.NoError(t, rows.Scan(&id, &l))
			langs[id] = l
		}
		require.NoError(t, rows.Close())
		for _, r := range jobs {
			if l := r.SourceLanguage(); l == "" {
				require.Nil(t, langs[r.ID], r.ID)
			} else {
				require.Equal(t, l, *langs[r.ID], r.ID)
			}
		}
		require.Nil(t, langs["bad"], "undecodable job is unknown")
		var filled int
		require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE id LIKE 'fill%' AND language = 'ru'`).Scan(&filled))
		require.Equal(t, 2*languageBatch+7, filled, "every batch is backfilled")

		require.Equal(t, before, pageIDs(t, s, "", "", "", 100), "the same jobs are listed after the upgrade")
		for _, lang := range []string{"de", "en", "fr", LanguageUnknown, "ru"} {
			require.Equal(t, want(jobs, "", "", lang), pageIDs(t, s, "", "", lang, 2), lang)
		}
		require.NoError(t, s.Close())
	}
}
