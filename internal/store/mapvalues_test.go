package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
)

func table(t *testing.T, rows ...string) *outlets.Table {
	t.Helper()
	csv := "domain,domains,hosts,name,country_code,country,bias,factuality,site_lang\n"
	for _, r := range rows {
		csv += r + "\n"
	}
	p := filepath.Join(t.TempDir(), "outlets.csv")
	require.NoError(t, os.WriteFile(p, []byte(csv), 0o600))
	tb, err := outlets.Load(p)
	require.NoError(t, err)
	return tb
}

const (
	spiegel = "spiegel.de,spiegel.de,spiegel.de,Der Spiegel,DE,Germany,center,high,de"
	nocc    = "nocc.example,nocc.example,nocc.example,No Country,,,center,high,en"
)

// mrec is a done public job analyzed at url with the given runs.
func mrec(id string, age int, url string, runs ...job.AnalyzerRun) *job.Record {
	r := rec(id, age, job.StatusCompleted)
	r.Input = &job.Input{Content: "c", ContentType: "text/plain", Source: job.Source{URL: url}}
	r.Analyzers = runs
	return r
}

func run(id, status, result string) job.AnalyzerRun {
	return job.AnalyzerRun{ID: id, Status: status, Result: json.RawMessage(result)}
}

func techs(names ...string) job.AnalyzerRun {
	d := ""
	for i, n := range names {
		if i > 0 {
			d += ","
		}
		d += fmt.Sprintf(`{"name":%q,"display":%q}`, n, n)
	}
	return run("techniques", job.RunSucceeded, `{"detected":[`+d+`]}`)
}

func pageOutletRun(cc string) job.AnalyzerRun {
	return run("source_info", job.RunSucceeded, fmt.Sprintf(`{"page_outlet":{"name":"P","domain":"p.x","country_code":%q,"country":"Pland"}}`, cc))
}

func TestDeriveMap(t *testing.T) {
	tb := table(t, spiegel, nocc)
	srcNull := run("source_info", job.RunSucceeded, `{"page_outlet":null}`)
	tests := []struct {
		name    string
		rec     *job.Record
		mutate  func(*job.Record)
		code    string
		country string
		techs   []string
	}{
		{name: "page_outlet wins over the table", rec: mrec("a", 0, "https://spiegel.de/x", pageOutletRun("RU"), techs("Doubt", "Loaded_Language")),
			code: "RU", country: "Pland", techs: []string{"Loaded_Language", "Doubt"}},
		{name: "fallback when source_info skipped", rec: mrec("a", 0, "https://www.spiegel.de/politik/a-1.html", run("source_info", job.RunSkipped, ""), techs("Doubt")),
			code: "DE", country: "Germany", techs: []string{"Doubt"}},
		{name: "fallback when page_outlet null", rec: mrec("a", 0, "https://spiegel.de/x", srcNull, techs("Slogans")), code: "DE", country: "Germany", techs: []string{"Slogans"}},
		{name: "unmatched URL", rec: mrec("a", 0, "https://unknown-blog.fr/post", techs("Doubt"))},
		{name: "no URL", rec: mrec("a", 0, "", techs("Doubt"))},
		{name: "page_outlet without country is none, no fallback", rec: mrec("a", 0, "https://spiegel.de/x", pageOutletRun(""), techs("Doubt"))},
		{name: "table outlet without country", rec: mrec("a", 0, "https://nocc.example/x", techs("Doubt"))},
		{name: "weak only counts", rec: mrec("a", 0, "https://spiegel.de/x", run("techniques", job.RunSucceeded, `{"detected":[{"name":"Bandwagon","display":"Bandwagon","strength":"weak"}]}`)),
			code: "DE", country: "Germany", techs: []string{"Bandwagon"}},
		{name: "failed techniques", rec: mrec("a", 0, "https://spiegel.de/x", run("techniques", job.RunFailed, ""), run("llm_techniques", job.RunSucceeded, `{"findings":[{"technique":"Doubt"}]}`))},
		{name: "llm_techniques only", rec: mrec("a", 0, "https://spiegel.de/x", run("llm_techniques", job.RunSucceeded, `{"findings":[{"technique":"Doubt"}]}`))},
		{name: "empty detected", rec: mrec("a", 0, "https://spiegel.de/x", techs())},
		{name: "unknown technique name", rec: mrec("a", 0, "https://spiegel.de/x", techs("Made_Up"))},
		{name: "duplicates collapse", rec: mrec("a", 0, "https://spiegel.de/x", techs("Doubt", "Doubt")), code: "DE", country: "Germany", techs: []string{"Doubt"}},
		{name: "not public", rec: mrec("a", 0, "https://spiegel.de/x", techs("Doubt")), mutate: func(r *job.Record) { r.Public = false }},
		{name: "not done", rec: mrec("a", 0, "https://spiegel.de/x", techs("Doubt")), mutate: func(r *job.Record) { r.Done = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mutate != nil {
				tc.mutate(tc.rec)
			}
			v := DeriveMap(&tc.rec.Job, tb)
			require.Equal(t, tc.code != "", v.Eligible)
			if tc.code != "" {
				require.Equal(t, []JobCountry{{tc.code, tc.country, []string{"outlet"}}}, v.Countries)
			} else {
				require.Empty(t, v.Countries)
			}
			var names []string
			for _, mt := range v.Techniques {
				names = append(names, mt.Name)
			}
			if tc.code != "" {
				require.Equal(t, tc.techs, names)
			}
		})
	}
}

func TestDeriveMapStrengthOnlyWhenStored(t *testing.T) {
	r := mrec("a", 0, "", pageOutletRun("DE"), run("techniques", job.RunSucceeded,
		`{"detected":[{"name":"Doubt","display":"Doubt","strength":"strong"},{"name":"Repetition","display":"Repetition"}]}`))
	b, err := json.Marshal(DeriveMap(&r.Job, nil).Techniques)
	require.NoError(t, err)
	require.JSONEq(t, `[{"name":"Repetition","display":"Repetition"},{"name":"Doubt","display":"Doubt","strength":"strong"}]`, string(b))
}

func openTemp(t *testing.T) *SQLite { return open(t, filepath.Join(t.TempDir(), "jobs.db")) }

func mapSet(t *testing.T) []*job.Record {
	return []*job.Record{
		mrec("ru1", 3, "", pageOutletRun("RU"), techs("Doubt", "Loaded_Language")),
		mrec("ru2", 2, "", pageOutletRun("RU"), techs("Loaded_Language")),
		mrec("de1", 1, "", pageOutletRun("DE"), techs("Slogans")),
		mrec("none", 0, "", pageOutletRun("DE"), techs()),
	}
}

// both stores answer the map queries alike, and a retention prune / eviction removes the map rows.
func TestMapQueriesMatchAcrossStores(t *testing.T) {
	sq := openTemp(t)
	for name, s := range map[string]Store{"memory": NewMemory(), "sqlite": sq} {
		t.Run(name, func(t *testing.T) {
			for _, r := range mapSet(t) {
				s.Put(r)
				save(t, s, r)
			}
			got, _, err := s.MapCountries()
			require.NoError(t, err)
			require.Equal(t, []MapCountry{
				{Code: "DE", Country: "Pland", Analyses: 1, Techniques: []TechniqueCount{{"Slogans", 1}}},
				{Code: "RU", Country: "Pland", Analyses: 2, Techniques: []TechniqueCount{{"Loaded_Language", 2}, {"Doubt", 1}}},
			}, got)
			page, err := s.MapAnalyses("RU", nil, 1)
			require.NoError(t, err)
			require.Len(t, page, 1)
			require.Equal(t, "ru2", page[0].Job.ID)
			require.Equal(t, []JobCountry{{"RU", "Pland", []string{"outlet"}}}, page[0].Values.Countries)
			rest, err := s.MapAnalyses("RU", &Cursor{CreatedAt: page[0].Job.CreatedAt, ID: page[0].Job.ID}, 0)
			require.NoError(t, err)
			require.Len(t, rest, 1)
			require.Equal(t, "ru1", rest[0].Job.ID)
			require.Equal(t, []string{"Loaded_Language", "Doubt"}, []string{rest[0].Values.Techniques[0].Name, rest[0].Values.Techniques[1].Name})
			none, err := s.MapAnalyses("FR", nil, 5)
			require.NoError(t, err)
			require.Empty(t, none)
		})
	}
}

func TestMapRemovedJobsLeaveTheMap(t *testing.T) {
	sq, mem := openTemp(t), NewMemory()
	old := t0.Add(-48 * time.Hour)
	for _, s := range []Store{mem, sq} {
		for _, r := range mapSet(t) {
			r.FinishedAt = &t0
			if r.ID == "ru1" {
				r.FinishedAt = &old
			}
			s.Put(r)
			save(t, s, r)
		}
	}
	require.NoError(t, sq.Prune(t0.Add(-24*time.Hour)))
	mem.EvictFinished(t0.Add(-24 * time.Hour))
	for _, s := range []Store{mem, sq} {
		got, _, err := s.MapCountries()
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.Equal(t, 1, got[1].Analyses, "ru1 is gone")
		l, err := s.MapAnalyses("RU", nil, 0)
		require.NoError(t, err)
		require.Len(t, l, 1)
	}
	var n int
	require.NoError(t, sq.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE map_eligible = 1`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestMapValuesFixedWhenDone(t *testing.T) {
	sq := openTemp(t)
	sq.SetOutlets(table(t, "gb.example,gb.example,gb.example,GB Out,GB,United Kingdom,center,high,en"))
	r := mrec("a", 0, "https://gb.example/x", techs("Doubt"))
	r.Done = false
	save(t, sq, r)
	r.Done = true
	save(t, sq, r)
	sq.SetOutlets(table(t, "gb.example,gb.example,gb.example,GB Out,IE,Ireland,center,high,en")) // the table changes later
	save(t, sq, r)                                                                               // saved again
	require.NoError(t, sq.BackfillMap())
	got, _, err := sq.MapCountries()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "GB", got[0].Code)
}

// preChangeDB builds a database at schema version 2 (before the map columns) with done and unfinished jobs.
func preChangeDB(t *testing.T, path string, recs ...*job.Record) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()
	for _, m := range migrations[:2] {
		_, err := db.Exec(m)
		require.NoError(t, err)
	}
	_, err = db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL); INSERT INTO schema_version VALUES (2)`)
	require.NoError(t, err)
	for _, r := range recs {
		data, err := json.Marshal(r.Clone())
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO jobs (id, status, done, created_at, finished_at, public, data, state) VALUES (?, ?, ?, ?, ?, ?, ?, '{}')`,
			r.ID, r.Status, r.Done, r.CreatedAt.UnixNano(), nanos(r.FinishedAt), r.Public, string(data))
		require.NoError(t, err)
	}
}

func TestMigrationAndBackfillOfOldJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	many := mapSet(t)
	for i := 0; i < backfillBatch+5; i++ { // more than one batch
		many = append(many, mrec(fmt.Sprintf("fi%03d", i), 10+i, "", pageOutletRun("FI"), techs("Doubt")))
	}
	running := rec("run", 0, job.StatusRunning)
	preChangeDB(t, path, append(many, running)...)

	s, err := OpenSQLite(path) // migrates; nothing is backfilled by opening
	require.NoError(t, err)
	got, _, err := s.MapCountries()
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, s.BackfillMap())
	require.NoError(t, s.BackfillMap(), "idempotent")
	got, _, err = s.MapCountries()
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, "FI", got[1].Code)
	require.Equal(t, backfillBatch+5, got[1].Analyses)
	require.NoError(t, s.Close())

	s, err = OpenSQLite(path) // reopening keeps the values
	require.NoError(t, err)
	defer s.Close()
	again, _, err := s.MapCountries()
	require.NoError(t, err)
	require.Equal(t, got, again)
	var n int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE map_eligible IS NULL`).Scan(&n))
	require.Equal(t, 1, n, "only the unfinished job has no map values")
}

func assoc(codes ...string) *job.CountryAssociation {
	var cs []job.AssociatedCountry
	for _, c := range codes {
		cs = append(cs, job.AssociatedCountry{CountryCode: c, Country: "N" + c})
	}
	return job.NewCountryAssociation(t0, cs, nil, nil)
}

func TestDeriveMapCountries(t *testing.T) {
	r := mrec("a", 0, "", pageOutletRun("DE"), techs("Doubt"))
	r.CountryAssociation = assoc("US", "DE")
	require.Equal(t, []JobCountry{{"DE", "Pland", []string{"outlet", "content"}}, {"US", "NUS", []string{"content"}}}, DeriveMap(&r.Job, nil).Countries)

	r.CountryAssociation = job.NewCountryAssociation(t0, nil, nil, &job.RunError{Code: job.CodeTimeout})
	require.Equal(t, []JobCountry{{"DE", "Pland", []string{"outlet"}}}, DeriveMap(&r.Job, nil).Countries, "failed: outlet only")

	pasted := mrec("p", 0, "", techs("Doubt"))
	pasted.CountryAssociation = assoc("US")
	v := DeriveMap(&pasted.Job, nil)
	require.True(t, v.Eligible)
	require.Nil(t, v.Outlet)
	require.Equal(t, []JobCountry{{"US", "NUS", []string{"content"}}}, v.Countries)

	pasted.CountryAssociation = assoc()
	require.False(t, DeriveMap(&pasted.Job, nil).Eligible, "no country at all")
	for _, cc := range []string{"", "DEU", "1A"} {
		bad := mrec("b", 0, "", pageOutletRun(cc), techs("Doubt"))
		v := DeriveMap(&bad.Job, nil)
		require.False(t, v.Eligible, cc)
		require.NotNil(t, v.Outlet, "the outlet is still the map outlet")
	}
}

// an analysis with several countries is counted and listed in each, once in the distinct total, in both stores.
func TestMultiCountryMapAcrossStores(t *testing.T) {
	a := mrec("a", 3, "", pageOutletRun("DE"), techs("Doubt"))
	a.CountryAssociation = assoc("US", "DE")
	b := mrec("b", 2, "", techs("Slogans")) // pasted text
	b.CountryAssociation = assoc("RU", "UA")
	c := mrec("c", 1, "", pageOutletRun("RU"), techs("Doubt"))
	for name, s := range map[string]Store{"memory": NewMemory(), "sqlite": openTemp(t)} {
		t.Run(name, func(t *testing.T) {
			for _, r := range []*job.Record{a, b, c} {
				s.Put(r)
				save(t, s, r)
			}
			got, total, err := s.MapCountries()
			require.NoError(t, err)
			require.Equal(t, 3, total)
			require.Equal(t, []MapCountry{
				{Code: "DE", Country: "Pland", Analyses: 1, Techniques: []TechniqueCount{{"Doubt", 1}}},
				{Code: "RU", Country: "Pland", Analyses: 2, Techniques: []TechniqueCount{{"Doubt", 1}, {"Slogans", 1}}},
				{Code: "UA", Country: "NUA", Analyses: 1, Techniques: []TechniqueCount{{"Slogans", 1}}},
				{Code: "US", Country: "NUS", Analyses: 1, Techniques: []TechniqueCount{{"Doubt", 1}}},
			}, got)
			ru, err := s.MapAnalyses("RU", nil, 0)
			require.NoError(t, err)
			require.Len(t, ru, 2)
			require.Equal(t, "c", ru[0].Job.ID)
			require.Equal(t, "b", ru[1].Job.ID)
			require.Nil(t, ru[1].Values.Outlet)
			require.Equal(t, []JobCountry{{"RU", "NRU", []string{"content"}}, {"UA", "NUA", []string{"content"}}}, ru[1].Values.Countries)
			us, err := s.MapAnalyses("US", nil, 0)
			require.NoError(t, err)
			require.Len(t, us, 1)
			require.Equal(t, []JobCountry{{"DE", "Pland", []string{"outlet", "content"}}, {"US", "NUS", []string{"content"}}}, us[0].Values.Countries)
		})
	}
}

// a database at schema version 3 (one map country per job) keeps that country as the outlet-basis map country.
func TestMigrationKeepsSingleMapCountry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.db")
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	for _, m := range migrations[:3] {
		_, err := db.Exec(m)
		require.NoError(t, err)
	}
	r := mrec("fi", 1, "", pageOutletRun("FI"), techs("Doubt"))
	data, err := json.Marshal(r.Clone())
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL); INSERT INTO schema_version VALUES (3);
		INSERT INTO jobs (id, status, done, created_at, finished_at, public, data, state, map_eligible, map_country_code, map_country, map_outlet, map_techniques)
		VALUES ('fi', 'completed', 1, ?, ?, 1, ?, '{}', 1, 'FI', 'Finland', '{"name":"P","domain":"p.x","country_code":"FI","country":"Finland"}', '[{"name":"Doubt","display":"Doubt"}]')`,
		r.CreatedAt.UnixNano(), r.FinishedAt.UnixNano(), string(data))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s := open(t, path)
	got, total, err := s.MapCountries()
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, []MapCountry{{Code: "FI", Country: "Finland", Analyses: 1, Techniques: []TechniqueCount{{"Doubt", 1}}}}, got)
	items, err := s.MapAnalyses("FI", nil, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, []JobCountry{{"FI", "Finland", []string{"outlet"}}}, items[0].Values.Countries)
}

// the backfill replaces only a missing or failed association and re-derives the countries with the fixed outlet.
func TestSetCountryAssociation(t *testing.T) {
	s := openTemp(t)
	s.SetOutlets(table(t, "gb.example,gb.example,gb.example,GB Out,GB,United Kingdom,center,high,en"))
	r := mrec("a", 0, "https://gb.example/x", techs("Doubt"))
	s.Put(r)
	save(t, s, r)
	s.SetOutlets(table(t, "gb.example,gb.example,gb.example,GB Out,IE,Ireland,center,high,en"))
	cand, err := s.CountryBackfill(10, 3)
	require.NoError(t, err)
	require.Len(t, cand, 1)

	set, err := s.SetCountryAssociation("a", assoc("GB", "IE"))
	require.NoError(t, err)
	require.True(t, set)
	require.Equal(t, assoc("GB", "IE"), r.CountryAssociation, "the live record follows")
	items, err := s.MapAnalyses("IE", nil, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, []JobCountry{{"GB", "United Kingdom", []string{"outlet", "content"}}, {"IE", "NIE", []string{"content"}}}, items[0].Values.Countries)
	loaded, _, err := s.Load("a")
	require.NoError(t, err)
	require.Equal(t, assoc("GB", "IE"), loaded.CountryAssociation)

	set, err = s.SetCountryAssociation("a", assoc("FR"))
	require.NoError(t, err)
	require.False(t, set, "a succeeded association never changes")
	cand, err = s.CountryBackfill(10, 3)
	require.NoError(t, err)
	require.Empty(t, cand)

	set, err = s.SetCountryAssociation("gone", assoc("FR"))
	require.NoError(t, err)
	require.False(t, set)
}
