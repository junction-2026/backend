package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, no CGO

	"github.com/junction-2026/backend/internal/job"
)

// migrations[i] upgrades schema version i to i+1. Append only; never edit a released entry.
var migrations = []string{
	`CREATE TABLE jobs (
		id          TEXT PRIMARY KEY,
		status      TEXT NOT NULL,
		done        INTEGER NOT NULL,
		created_at  INTEGER NOT NULL, -- unix nanoseconds (sorts like the time, unlike RFC 3339 text)
		finished_at INTEGER,
		data        TEXT NOT NULL,    -- the Job as JSON
		state       TEXT NOT NULL     -- plan, progress, previews, activities, last_seq as JSON
	);
	CREATE INDEX jobs_created ON jobs (created_at DESC, id);
	CREATE INDEX jobs_status_created ON jobs (status, created_at DESC, id);
	CREATE INDEX jobs_unfinished ON jobs (created_at) WHERE done = 0;
	CREATE INDEX jobs_finished ON jobs (finished_at) WHERE done = 1;`,
	// public: job visibility (unlisted jobs are left out of Page); rows stored before it are public.
	`ALTER TABLE jobs ADD COLUMN public INTEGER NOT NULL DEFAULT 1;
	CREATE INDEX jobs_public_created ON jobs (public, created_at DESC, id);
	CREATE INDEX jobs_public_status_created ON jobs (public, status, created_at DESC, id);`,
}

// SQLite is a Store whose history lives in a SQLite file. Its working set is the embedded Memory store.
type SQLite struct {
	*Memory
	db      *sql.DB
	wmu     sync.Mutex   // one writer at a time
	decoded atomic.Int64 // jobs decoded by Page (test seam)
}

// OpenSQLite opens (creating it and its directory if needed) and migrates the database at path.
func OpenSQLite(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("creating the directory of the job store: %w", err)
	}
	q := url.Values{"_pragma": {"journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"}, "_txlock": {"immediate"}}
	u := url.URL{Scheme: "file", Opaque: (&url.URL{Path: path}).EscapedPath(), RawQuery: q.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("opening the job store: %w", err)
	}
	s := &SQLite{Memory: NewMemory(), db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLite) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("opening the job store: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("migrating the job store: %w", err)
	}
	var v int
	switch err := tx.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v); {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("migrating the job store: %w", err)
		}
	case err != nil:
		return fmt.Errorf("migrating the job store: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("the job store has schema version %d, newer than the %d this server supports", v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			return fmt.Errorf("migrating the job store to version %d: %w", v+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, v); err != nil {
		return fmt.Errorf("migrating the job store: %w", err)
	}
	return tx.Commit()
}

func (s *SQLite) Close() error { return s.db.Close() }

// state is the event-stream state column. Previews are kept as raw JSON so they read back verbatim.
type state struct {
	Plan       *job.Plan               `json:"plan"`
	Progress   *job.Progress           `json:"progress"`
	Previews   json.RawMessage         `json:"previews"`
	Activities map[string]job.Activity `json:"activities"`
	LastSeq    int64                   `json:"last_seq"`
}

func nanos(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

// Save upserts the job; j is serialized before it returns, so the caller's lock covers a consistent state.
func (s *SQLite) Save(j *job.Record) error {
	data, err := json.Marshal(j.Clone()) // the same value GET /jobs/{id} returns
	if err != nil {
		return fmt.Errorf("encoding job: %w", err)
	}
	previews, err := json.Marshal(j.Previews)
	if err != nil {
		return fmt.Errorf("encoding job previews: %w", err)
	}
	st, err := json.Marshal(state{Plan: j.Plan, Progress: j.Progress, Previews: previews, Activities: j.Activities, LastSeq: j.LastSeq})
	if err != nil {
		return fmt.Errorf("encoding job state: %w", err)
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err = s.db.Exec(`INSERT INTO jobs (id, status, done, created_at, finished_at, public, data, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, done = excluded.done, finished_at = excluded.finished_at,
		data = excluded.data, state = excluded.state`,
		j.ID, j.Status, j.Done, j.CreatedAt.UnixNano(), nanos(j.FinishedAt), j.Public, string(data), string(st))
	return err
}

// decode builds an unshared record from the data and state columns.
func decode(data, st []byte) (*job.Record, error) {
	r := &job.Record{}
	if err := json.Unmarshal(data, &r.Job); err != nil {
		return nil, fmt.Errorf("decoding job: %w", err)
	}
	for i := range r.Analyzers { // a null result reads back as the nil it was stored from
		if string(r.Analyzers[i].Result) == "null" {
			r.Analyzers[i].Result = nil
		}
	}
	if string(r.Finalization.Result) == "null" {
		r.Finalization.Result = nil
	}
	var s state
	if err := json.Unmarshal(st, &s); err != nil {
		return nil, fmt.Errorf("decoding job state: %w", err)
	}
	r.Plan, r.Progress, r.Activities, r.LastSeq = s.Plan, s.Progress, s.Activities, s.LastSeq
	if r.Activities == nil {
		r.Activities = map[string]job.Activity{}
	}
	var previews map[string]json.RawMessage
	if err := json.Unmarshal(s.Previews, &previews); err != nil {
		return nil, fmt.Errorf("decoding job previews: %w", err)
	}
	r.Previews = make(map[string]any, len(previews))
	for k, v := range previews {
		r.Previews[k] = v
	}
	return r, nil
}

func (s *SQLite) Load(id string) (*job.Record, bool, error) {
	var data, st []byte
	switch err := s.db.QueryRow(`SELECT data, state FROM jobs WHERE id = ?`, id).Scan(&data, &st); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	r, err := decode(data, st)
	return r, err == nil, err
}

func (s *SQLite) Unfinished() ([]*job.Record, error) {
	rows, err := s.db.Query(`SELECT data, state FROM jobs WHERE done = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*job.Record
	for rows.Next() {
		var data, st []byte
		if err := rows.Scan(&data, &st); err != nil {
			return nil, err
		}
		r, err := decode(data, st)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) Page(status string, after *Cursor, limit int) ([]job.Job, error) {
	where, args := []string{"public = 1"}, []any{}
	if status != "" {
		where, args = append(where, "status = ?"), append(args, status)
	}
	if after != nil {
		at := after.CreatedAt.UnixNano()
		where = append(where, "created_at <= ? AND (created_at < ? OR id > ?)") // the first term lets the index seek
		args = append(args, at, at, after.ID)
	}
	q := `SELECT data FROM jobs WHERE ` + strings.Join(where, " AND ")
	q += ` ORDER BY created_at DESC, id`
	if limit > 0 {
		q, args = q+` LIMIT ?`, append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []job.Job
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var j job.Job
		if err := json.Unmarshal(data, &j); err != nil {
			return nil, fmt.Errorf("decoding job: %w", err)
		}
		s.decoded.Add(1)
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *SQLite) Prune(cutoff time.Time) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.db.Exec(`DELETE FROM jobs WHERE done = 1 AND finished_at < ?`, cutoff.UnixNano())
	return err
}
