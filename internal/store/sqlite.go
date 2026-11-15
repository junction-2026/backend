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
	"github.com/junction-2026/backend/internal/outlets"
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
	// map values (propaganda map): fixed when the job becomes done. map_eligible is NULL until they are determined
	// (not done yet, or stored before this version and not backfilled yet); 1 for a public done job with detected
	// techniques and a map country, else 0.
	`ALTER TABLE jobs ADD COLUMN map_eligible INTEGER;
	ALTER TABLE jobs ADD COLUMN map_country_code TEXT NOT NULL DEFAULT '';
	ALTER TABLE jobs ADD COLUMN map_country TEXT NOT NULL DEFAULT '';
	ALTER TABLE jobs ADD COLUMN map_outlet TEXT;      -- the map Outlet as JSON
	ALTER TABLE jobs ADD COLUMN map_techniques TEXT;  -- [{name, display, strength?}] as JSON
	CREATE INDEX jobs_map ON jobs (map_country_code, created_at DESC, id) WHERE map_eligible = 1;`,
	// multi-country map (country association): the map countries of an eligible job, one row each, indexed for the
	// per-country listing; map_countries is the same list as JSON for reading. map_outlet and map_techniques are now
	// also kept for a done public job with detected propaganda but no map country (map_eligible 0), so the backfill
	// can determine its countries again; SQL NULL there means not recorded. The single map country of existing rows
	// becomes their outlet-basis map country; map_country_code and map_country are no longer read.
	// country_attempts counts the country association backfill attempts of the job.
	`CREATE TABLE job_map_countries (
		job_id       TEXT NOT NULL,
		position     INTEGER NOT NULL,
		country_code TEXT NOT NULL,
		country      TEXT NOT NULL,
		created_at   INTEGER NOT NULL, -- the job's, for the listing order
		PRIMARY KEY (job_id, position)
	);
	CREATE INDEX job_map_countries_list ON job_map_countries (country_code, created_at DESC, job_id);
	ALTER TABLE jobs ADD COLUMN map_countries TEXT; -- [{country_code, country, basis}] as JSON
	ALTER TABLE jobs ADD COLUMN country_attempts INTEGER NOT NULL DEFAULT 0;
	INSERT INTO job_map_countries (job_id, position, country_code, country, created_at)
		SELECT id, 0, map_country_code, map_country, created_at FROM jobs WHERE map_eligible = 1;
	UPDATE jobs SET map_countries = json_array(json_object('country_code', map_country_code, 'country', map_country, 'basis', json_array('outlet')))
		WHERE map_eligible = 1;
	DROP INDEX jobs_map;`,
	// language: the job language (job.Job.SourceLanguage), NULL when unknown; set by every save. Existing rows are
	// filled by backfillLanguage in the same transaction.
	`ALTER TABLE jobs ADD COLUMN language TEXT;
	CREATE INDEX jobs_public_language_created ON jobs (public, language, created_at DESC, id);`,
}

// backfills[i], when set, runs right after migrations[i] in the same transaction.
var backfills = map[int]func(context.Context, *sql.Tx) error{4: backfillLanguage}

// languageBatch is the number of rows backfillLanguage reads at a time.
const languageBatch = 500

// backfillLanguage sets the language of every row from its stored job, in batches by id. A row whose job cannot be
// decoded keeps NULL (unknown).
func backfillLanguage(ctx context.Context, tx *sql.Tx) error {
	type row struct {
		id   string
		data []byte
	}
	for last := ""; ; {
		rows, err := tx.QueryContext(ctx, `SELECT id, data FROM jobs WHERE id > ? ORDER BY id LIMIT ?`, last, languageBatch)
		if err != nil {
			return err
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.data); err != nil {
				_ = rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			var j job.Job
			if json.Unmarshal(r.data, &j) != nil {
				continue
			}
			if l := j.SourceLanguage(); l != "" {
				if _, err := tx.ExecContext(ctx, `UPDATE jobs SET language = ? WHERE id = ?`, l, r.id); err != nil {
					return err
				}
			}
		}
		last = batch[len(batch)-1].id
	}
}

// nullable is s, or SQL NULL when s is "".
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
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
		if f := backfills[v]; f != nil {
			if err := f(ctx, tx); err != nil {
				return fmt.Errorf("migrating the job store to version %d: %w", v+1, err)
			}
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
	var mv MapValues
	if j.Done {
		mv = DeriveMap(&j.Job, s.table())
	}
	lang := j.SourceLanguage()
	return s.write(func(tx *sql.Tx) error {
		// The map values are written once, by the first save of the done job; later saves keep them.
		fix := false
		if j.Done {
			var eligible sql.NullInt64
			if err := tx.QueryRow(`SELECT map_eligible FROM jobs WHERE id = ?`, j.ID).Scan(&eligible); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			fix = !eligible.Valid
		}
		if _, err := tx.Exec(`INSERT INTO jobs (id, status, done, created_at, finished_at, public, language, data, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET status = excluded.status, done = excluded.done, finished_at = excluded.finished_at,
			language = excluded.language, data = excluded.data, state = excluded.state`,
			j.ID, j.Status, j.Done, j.CreatedAt.UnixNano(), nanos(j.FinishedAt), j.Public, nullable(lang), string(data), string(st)); err != nil {
			return err
		}
		if !fix {
			return nil
		}
		_, err := writeMap(tx, j.ID, j.CreatedAt, mv, "")
		return err
	})
}

// write runs f in a write transaction (one writer at a time).
func (s *SQLite) write(f func(tx *sql.Tx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// writeMap sets the map columns of job id to v and replaces its job_map_countries rows; cond (may be "") restricts the
// update, then nothing is written when it does not hold. It reports whether the row was updated.
func writeMap(tx *sql.Tx, id string, createdAt time.Time, v MapValues, cond string, condArgs ...any) (bool, error) {
	cols, err := mapColumns(v)
	if err != nil {
		return false, err
	}
	q := `UPDATE jobs SET map_eligible = ?, map_outlet = ?, map_techniques = ?, map_countries = ? WHERE id = ?`
	if cond != "" {
		q += ` AND ` + cond
	}
	res, err := tx.Exec(q, append(append(cols, id), condArgs...)...)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM job_map_countries WHERE job_id = ?`, id); err != nil {
		return false, err
	}
	for i, c := range v.Countries {
		if _, err := tx.Exec(`INSERT INTO job_map_countries (job_id, position, country_code, country, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, i, c.Code, c.Country, createdAt.UnixNano()); err != nil {
			return false, err
		}
	}
	return true, nil
}

// mapColumns is v as the values of map_eligible, map_outlet, map_techniques and map_countries. Outlet and techniques
// are kept whenever the job has detected propaganda (a JSON null outlet: none), so a backfill can reuse them.
func mapColumns(v MapValues) ([]any, error) {
	eligible := 0
	if v.Eligible {
		eligible = 1
	}
	if v.Techniques == nil {
		return []any{eligible, nil, nil, nil}, nil
	}
	outlet, err := json.Marshal(v.Outlet)
	if err != nil {
		return nil, fmt.Errorf("encoding map outlet: %w", err)
	}
	techniques, err := json.Marshal(v.Techniques)
	if err != nil {
		return nil, fmt.Errorf("encoding map techniques: %w", err)
	}
	var countries any
	if v.Eligible {
		b, err := json.Marshal(v.Countries)
		if err != nil {
			return nil, fmt.Errorf("encoding map countries: %w", err)
		}
		countries = string(b)
	}
	return []any{eligible, string(outlet), string(techniques), countries}, nil
}

// backfillBatch is the number of jobs BackfillMap derives per transaction.
const backfillBatch = 200

// BackfillMap determines the map values of every done job that has none (stored before the map) from its stored
// results and the outlet table set now, in batches; idempotent. A job whose data cannot be decoded is not a map analysis.
func (s *SQLite) BackfillMap() error {
	table := s.table()
	for {
		rows, err := s.db.Query(`SELECT id, data FROM jobs WHERE done = 1 AND map_eligible IS NULL LIMIT ?`, backfillBatch)
		if err != nil {
			return fmt.Errorf("backfilling map values: %w", err)
		}
		type row struct {
			id   string
			data []byte
		}
		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.data); err != nil {
				_ = rows.Close()
				return fmt.Errorf("backfilling map values: %w", err)
			}
			batch = append(batch, r)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return fmt.Errorf("backfilling map values: %w", err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := s.write(func(tx *sql.Tx) error {
			for _, r := range batch {
				var j job.Job
				var v MapValues
				if json.Unmarshal(r.data, &j) == nil {
					v = DeriveMap(&j, table)
				}
				// map_eligible IS NULL: a save of the done job meanwhile has already fixed the values
				if _, err := writeMap(tx, r.id, j.CreatedAt, v, `map_eligible IS NULL`); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fmt.Errorf("backfilling map values: %w", err)
		}
	}
}

// missingAssociation holds for a row whose country_association is absent, null or failed.
const missingAssociation = `(json_extract(data, '$.country_association') IS NULL OR json_extract(data, '$.country_association.status') = 'failed')`

// CountryBackfill: see Store.
// shortcut: scans the done public rows' JSON on every batch; add a pending-flag column with an index if history grows large.
func (s *SQLite) CountryBackfill(limit, maxAttempts int) ([]job.Job, error) {
	rows, err := s.db.Query(`SELECT data FROM jobs WHERE done = 1 AND public = 1 AND country_attempts < ? AND `+missingAssociation+`
		AND EXISTS (SELECT 1 FROM json_each(data, '$.analyzers') a WHERE json_extract(a.value, '$.id') = 'techniques'
			AND json_extract(a.value, '$.status') = 'succeeded' AND json_array_length(a.value, '$.result.detected') > 0)
		ORDER BY country_attempts, created_at DESC, id LIMIT ?`, maxAttempts, limit)
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
		r, err := decode(data, []byte(`{"previews":{}}`))
		if err != nil {
			return nil, err
		}
		out = append(out, r.Job)
	}
	return out, rows.Err()
}

// SetCountryAssociation: see Store. The live record of the job, when in the working set, is updated under its lock.
func (s *SQLite) SetCountryAssociation(id string, a *job.CountryAssociation) (bool, error) {
	if live, ok := s.Memory.Get(id); ok {
		live.Lock()
		defer live.Unlock()
	}
	set := false
	err := s.write(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE jobs SET country_attempts = country_attempts + 1 WHERE id = ?`, id); err != nil {
			return err
		}
		var data []byte
		var outlet sql.NullString
		switch err := tx.QueryRow(`SELECT data, map_outlet FROM jobs WHERE id = ? AND `+missingAssociation, id).Scan(&data, &outlet); {
		case errors.Is(err, sql.ErrNoRows) || a == nil: // pruned, settled meanwhile, or only an attempt to count
			return nil
		case err != nil:
			return err
		}
		var j job.Job
		if err := json.Unmarshal(data, &j); err != nil {
			return fmt.Errorf("decoding job: %w", err)
		}
		j.CountryAssociation = a
		// the map outlet fixed when the job became done; shortcut: rows stored before map_outlet was kept for
		// non-eligible jobs take it from the current outlet table.
		v := deriveMap(&j, func() *outlets.Outlet {
			var o *outlets.Outlet
			if !outlet.Valid || json.Unmarshal([]byte(outlet.String), &o) != nil {
				return mapOutlet(&j, s.table())
			}
			return o
		})
		b, err := json.Marshal(&j)
		if err != nil {
			return fmt.Errorf("encoding job: %w", err)
		}
		if _, err := tx.Exec(`UPDATE jobs SET data = ? WHERE id = ?`, string(b), id); err != nil {
			return err
		}
		set, err = writeMap(tx, id, j.CreatedAt, v, "")
		return err
	})
	if err != nil || !set {
		return false, err
	}
	if live, ok := s.Memory.Get(id); ok { // locked above
		live.CountryAssociation = a
	}
	return true, nil
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

func (s *SQLite) Page(status, search, lang string, after *Cursor, limit int) ([]job.Job, error) {
	where, args := []string{"public = 1"}, []any{}
	if status != "" {
		where, args = append(where, "status = ?"), append(args, status)
	}
	switch lang {
	case "":
	case LanguageUnknown:
		where = append(where, "language IS NULL")
	default:
		where, args = append(where, "language = ?"), append(args, lang)
	}
	if after != nil {
		at := after.CreatedAt.UnixNano()
		where = append(where, "created_at <= ? AND (created_at < ? OR id > ?)") // the first term lets the index seek
		args = append(args, at, at, after.ID)
	}
	q := `SELECT data FROM jobs WHERE ` + strings.Join(where, " AND ")
	q += ` ORDER BY created_at DESC, id`
	if limit > 0 && search == "" {
		q, args = q+` LIMIT ?`, append(args, limit)
	}
	// a search is matched in Go (SQLite lower() is ASCII-only): scan in order until limit matches or the rows end
	search = strings.ToLower(search)
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
		if !Matches(&j, search) {
			continue
		}
		if out = append(out, j); limit > 0 && len(out) == limit {
			break
		}
	}
	return out, rows.Err()
}

func (s *SQLite) Prune(cutoff time.Time) error {
	return s.write(func(tx *sql.Tx) error {
		at := cutoff.UnixNano()
		if _, err := tx.Exec(`DELETE FROM job_map_countries WHERE job_id IN (SELECT id FROM jobs WHERE done = 1 AND finished_at < ?)`, at); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM jobs WHERE done = 1 AND finished_at < ?`, at)
		return err
	})
}

func (s *SQLite) MapCountries() ([]MapCountry, int, error) {
	a := newMapAgg()
	// the country name is that of the newest analysis of the code (the first of the listing order)
	rows, err := s.db.Query(`SELECT c.country_code, COUNT(*),
		(SELECT n.country FROM job_map_countries n WHERE n.country_code = c.country_code ORDER BY n.created_at DESC, n.job_id LIMIT 1)
		FROM job_map_countries c GROUP BY c.country_code`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var code, country string
		var n int
		if err := rows.Scan(&code, &n, &country); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		a.country(code).Analyses, a.country(code).Country = n, country
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, 0, err
	}
	rows, err = s.db.Query(`SELECT c.country_code, json_extract(t.value, '$.name'), COUNT(*)
		FROM job_map_countries c JOIN jobs j ON j.id = c.job_id, json_each(j.map_techniques) t GROUP BY 1, 2`)
	if err != nil {
		return nil, 0, err
	}
	for rows.Next() {
		var code, name string
		var n int
		if err := rows.Scan(&code, &name, &n); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		if a.counts[code] != nil { // a job that finished between the queries is counted by the next request
			a.counts[code][name] = n
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE map_eligible = 1`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return a.result(), total, nil
}

func (s *SQLite) MapAnalyses(code string, after *Cursor, limit int) ([]MapItem, error) {
	where, args := `c.country_code = ?`, []any{code}
	if after != nil {
		at := after.CreatedAt.UnixNano()
		where += ` AND c.created_at <= ? AND (c.created_at < ? OR c.job_id > ?)` // the first term lets the index seek
		args = append(args, at, at, after.ID)
	}
	q := `SELECT j.data, j.map_outlet, j.map_techniques, j.map_countries FROM job_map_countries c JOIN jobs j ON j.id = c.job_id
		WHERE ` + where + ` ORDER BY c.created_at DESC, c.job_id`
	if limit > 0 {
		q, args = q+` LIMIT ?`, append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapItem
	for rows.Next() {
		var data, outlet, techniques, countries []byte
		it := MapItem{Values: MapValues{Eligible: true}}
		if err := rows.Scan(&data, &outlet, &techniques, &countries); err != nil {
			return nil, err
		}
		if err := errors.Join(json.Unmarshal(data, &it.Job), json.Unmarshal(outlet, &it.Values.Outlet),
			json.Unmarshal(techniques, &it.Values.Techniques), json.Unmarshal(countries, &it.Values.Countries)); err != nil {
			return nil, fmt.Errorf("decoding map analysis: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
