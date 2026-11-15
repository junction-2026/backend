package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/store"
)

// mapJob is a done public job whose source_info found a page outlet in cc and whose techniques detected the names
// ("Name" or "Name:strength").
func mapJob(id string, age int, cc string, detected ...string) *job.Record {
	at := t0.Add(-time.Duration(age) * time.Second)
	var det []string
	for _, d := range detected {
		name, strength, _ := strings.Cut(d, ":")
		s := fmt.Sprintf(`{"name":%q,"display":"disp-%s"`, name, name)
		if strength != "" {
			s += fmt.Sprintf(`,"strength":%q`, strength)
		}
		det = append(det, s+"}")
	}
	outlet := `{"name":"Out ` + cc + `","domain":"x.` + strings.ToLower(cc) + `","country_code":"` + cc + `","country":"Land ` + cc + `"}`
	return &job.Record{Job: job.Job{ID: id, Status: job.StatusCompleted, Done: true, Public: true, CreatedAt: at, FinishedAt: &at,
		Input: &job.Input{Content: "text", ContentType: "text/plain", Source: job.Source{URL: "https://x." + strings.ToLower(cc) + "/a", Title: "T " + id}},
		Analyzers: []job.AnalyzerRun{
			{ID: "source_info", Status: job.RunSucceeded, Result: json.RawMessage(`{"page_outlet":` + outlet + `}`)},
			{ID: "techniques", Status: job.RunSucceeded, Result: json.RawMessage(`{"detected":[` + strings.Join(det, ",") + `]}`)},
		},
		Finalization: job.Finalization{Status: job.RunSkipped}}}
}

func mapHarness(t *testing.T, recs ...*job.Record) *harness {
	t.Helper()
	x := newHarness(t)
	x.svc.maps = store.NewMemory()
	for _, r := range recs {
		x.svc.maps.Put(r)
		require.NoError(t, x.svc.maps.Save(r))
	}
	return x
}

type mapPage struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

func (x *harness) page(t *testing.T, path string) mapPage {
	t.Helper()
	rec := x.do("GET", path, "")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var p mapPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

func TestMapCountries(t *testing.T) {
	x := mapHarness(t,
		mapJob("a", 5, "RU", "Loaded_Language", "Doubt"), mapJob("b", 4, "RU", "Doubt:weak", "Loaded_Language"),
		mapJob("c", 3, "DE", "Slogans"), mapJob("d", 2, "RU", "Repetition"))
	rec := x.do("GET", "/api/v1/map/countries?ignored=1", "")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"analyses":4,"items":[
		{"country_code":"DE","country":"Land DE","analyses":1,"techniques":[{"name":"Slogans","display":"Slogans","analyses":1}]},
		{"country_code":"RU","country":"Land RU","analyses":3,"techniques":[
			{"name":"Loaded_Language","display":"Loaded language","analyses":2},
			{"name":"Doubt","display":"Doubt","analyses":2},
			{"name":"Repetition","display":"Repetition","analyses":1}]}]}`, rec.Body.String())
}

func TestMapCountriesEmptyAndMethods(t *testing.T) {
	x := newHarness(t)
	rec := x.do("GET", "/api/v1/map/countries", "")
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"items":[],"analyses":0}`, rec.Body.String())
	for _, p := range []string{"/api/v1/map/countries", "/api/v1/map/countries/RU/analyses"} {
		for _, m := range []string{"POST", "PUT", "DELETE"} {
			wantError(t, x.do(m, p, "{}"), 405, "method_not_allowed")
		}
	}
}

func TestMapAnalysesPagingAndAggregateAgree(t *testing.T) {
	var recs []*job.Record
	for i := 0; i < 25; i++ {
		recs = append(recs, mapJob(fmt.Sprintf("j%02d", i), i, "RU", "Doubt"))
	}
	recs = append(recs, mapJob("tie-b", 0, "RU", "Doubt"), mapJob("tie-a", 0, "RU", "Doubt"))
	x := mapHarness(t, append(recs, mapJob("de", 1, "DE", "Doubt"))...)

	p1 := x.page(t, "/api/v1/map/countries/RU/analyses")
	require.Len(t, p1.Items, 20)
	require.NotNil(t, p1.NextCursor)
	p2 := x.page(t, "/api/v1/map/countries/ru/analyses?cursor="+*p1.NextCursor)
	require.Len(t, p2.Items, 7)
	require.Nil(t, p2.NextCursor)

	var ids []string
	for _, it := range append(p1.Items, p2.Items...) {
		ids = append(ids, it["job_id"].(string))
	}
	require.Equal(t, []string{"j00", "tie-a", "tie-b", "j01", "j02"}, ids[:5], "newest first, ties by id")
	require.Len(t, ids, 27)

	var agg struct {
		Items []struct {
			CountryCode string `json:"country_code"`
			Analyses    int    `json:"analyses"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(x.do("GET", "/api/v1/map/countries", "").Body.Bytes(), &agg))
	require.Equal(t, 27, agg.Items[1].Analyses)
	require.Equal(t, len(ids), agg.Items[1].Analyses)
}

func TestMapAnalysesValidation(t *testing.T) {
	x := mapHarness(t, mapJob("a", 1, "DE", "Doubt"))
	for _, p := range []string{"DEU/analyses", "1A/analyses", "D/analyses", "D%C3%89/analyses", "RU/analyses?limit=0", "RU/analyses?limit=101",
		"RU/analyses?limit=x", "RU/analyses?cursor=!!", "RU/analyses?cursor=" + "bm9wZQ"} {
		wantError(t, x.do("GET", "/api/v1/map/countries/"+p, ""), 400, "invalid_request")
	}
	require.Len(t, x.page(t, "/api/v1/map/countries/de/analyses?limit=100").Items, 1)
	empty := x.do("GET", "/api/v1/map/countries/FR/analyses", "")
	require.Equal(t, 200, empty.Code)
	require.JSONEq(t, `{"items":[],"next_cursor":null}`, empty.Body.String())
	require.JSONEq(t, `{"items":[],"next_cursor":null}`, newHarness(t).do("GET", "/api/v1/map/countries/FR/analyses", "").Body.String())
}

func TestMapAnalysisMembers(t *testing.T) {
	r := mapJob("j-42", 1, "DE", "Doubt:strong", "Repetition")
	r.Input.Content = "  Short\n\ntext "
	r.Finalization = job.Finalization{Status: job.RunSucceeded, Result: json.RawMessage(`{"verdict":"detected"}`)}
	nofin := mapJob("j-43", 2, "DE", "Slogans")
	nofin.Input.Source.Title = ""
	x := mapHarness(t, r, nofin)
	rec := x.do("GET", "/api/v1/map/countries/DE/analyses", "")
	var got struct{ Items []json.RawMessage }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.JSONEq(t, fmt.Sprintf(`{"job_id":"j-42","created_at":%q,"finished_at":%q,"status":"completed","country_code":"DE","country":"Land DE",
		"countries":[{"country_code":"DE","country":"Land DE","basis":["outlet"]}],
		"outlet":{"name":"Out DE","domain":"x.de","country_code":"DE","country":"Land DE","bias":"","factuality":""},
		"source":{"url":"https://x.de/a","title":"T j-42"},
		"techniques":[{"name":"Repetition","display":"disp-Repetition"},{"name":"Doubt","display":"disp-Doubt","strength":"strong"}],
		"verdict":"detected","snippet":"Short text"}`, r.CreatedAt.Format(time.RFC3339Nano), r.FinishedAt.Format(time.RFC3339Nano)), string(got.Items[0]))
	var second map[string]any
	require.NoError(t, json.Unmarshal(got.Items[1], &second))
	require.Nil(t, second["verdict"])
	require.Nil(t, second["source"].(map[string]any)["title"])
}

func TestMapSnippet(t *testing.T) {
	require.Equal(t, "Short text", snippet("  Short\n\ntext "))
	require.Equal(t, "", snippet(" \n\t"))
	long := strings.Repeat("ö", 200)
	require.Equal(t, long, snippet(long+"  \n"), "trailing whitespace is not a cut")
	got := snippet(long + "x")
	require.Equal(t, long+"…", got)
	require.Equal(t, 201, utf8.RuneCountInString(got))
	words := snippet(strings.Repeat("ab \n", 5000))
	require.True(t, strings.HasSuffix(words, "…"))
	require.Equal(t, 201, utf8.RuneCountInString(words))
	require.NotContains(t, words, "  ")
}

func TestMapEndpointsAreReadOnly(t *testing.T) {
	x := mapHarness(t, mapJob("a", 1, "DE", "Doubt"))
	before := x.svc.last
	x.do("GET", "/api/v1/map/countries", "")
	x.do("GET", "/api/v1/map/countries/DE/analyses", "")
	require.Equal(t, before, x.svc.last)
	require.Empty(t, x.svc.jobs)
}

func TestMapMultiCountryAndPastedText(t *testing.T) {
	multi := mapJob("multi", 2, "DE", "Doubt")
	multi.CountryAssociation = job.NewCountryAssociation(t0, []job.AssociatedCountry{{CountryCode: "US", Country: "United States"}, {CountryCode: "DE", Country: "Germany"}}, nil, nil)
	pasted := mapJob("pasted", 1, "XX", "Doubt")
	pasted.Analyzers = pasted.Analyzers[1:] // no source_info
	pasted.Input.Source = job.Source{}
	pasted.CountryAssociation = job.NewCountryAssociation(t0, []job.AssociatedCountry{{CountryCode: "US", Country: "United States"}}, nil, nil)
	x := mapHarness(t, multi, pasted)

	rec := x.do("GET", "/api/v1/map/countries", "")
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"analyses":2,"items":[
		{"country_code":"DE","country":"Land DE","analyses":1,"techniques":[{"name":"Doubt","display":"Doubt","analyses":1}]},
		{"country_code":"US","country":"United States","analyses":2,"techniques":[{"name":"Doubt","display":"Doubt","analyses":2}]}]}`, rec.Body.String())

	us := x.page(t, "/api/v1/map/countries/US/analyses")
	require.Len(t, us.Items, 2)
	p, m := us.Items[0], us.Items[1]
	require.Equal(t, "pasted", p["job_id"])
	require.Nil(t, p["outlet"])
	require.Equal(t, map[string]any{"url": "", "title": nil}, p["source"])
	require.Equal(t, "US", p["country_code"])
	require.Equal(t, []any{map[string]any{"country_code": "US", "country": "United States", "basis": []any{"content"}}}, p["countries"])

	require.Equal(t, "multi", m["job_id"])
	require.Equal(t, "US", m["country_code"], "the requested list's country")
	require.Equal(t, "United States", m["country"])
	require.Equal(t, []any{
		map[string]any{"country_code": "DE", "country": "Land DE", "basis": []any{"outlet", "content"}},
		map[string]any{"country_code": "US", "country": "United States", "basis": []any{"content"}},
	}, m["countries"])
	de := x.page(t, "/api/v1/map/countries/DE/analyses")
	require.Len(t, de.Items, 1)
	require.Equal(t, "DE", de.Items[0]["country_code"])
	require.Equal(t, "Land DE", de.Items[0]["country"])
}
