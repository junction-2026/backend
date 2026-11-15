package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/outlets"
	"github.com/junction-2026/backend/internal/store"
)

// Propaganda map (propaganda-map-api): read-only views of the stored map values; no analyzer, scraper or network call.

type techniqueCount struct {
	Name     string `json:"name"`
	Display  string `json:"display"`
	Analyses int    `json:"analyses"`
}

type countryAggregate struct {
	CountryCode string           `json:"country_code"`
	Country     string           `json:"country"`
	Analyses    int              `json:"analyses"`
	Techniques  []techniqueCount `json:"techniques"`
}

func (a *api) mapCountries(w http.ResponseWriter, _ *http.Request) {
	countries, total, err := a.svc.MapCountries()
	if err != nil {
		a.internal(w, "map countries", err)
		return
	}
	items := make([]countryAggregate, 0, len(countries))
	for _, c := range countries {
		agg := countryAggregate{CountryCode: c.Code, Country: c.Country, Analyses: c.Analyses, Techniques: make([]techniqueCount, 0, len(c.Techniques))}
		for _, t := range c.Techniques {
			agg.Techniques = append(agg.Techniques, techniqueCount{t.Name, adapter.TechniqueDisplay(t.Name), t.Analyses})
		}
		items = append(items, agg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "analyses": total})
}

type mapSource struct {
	URL   string  `json:"url"`
	Title *string `json:"title"`
}

type mapAnalysis struct {
	JobID       string               `json:"job_id"`
	CreatedAt   time.Time            `json:"created_at"`
	FinishedAt  *time.Time           `json:"finished_at"`
	Status      string               `json:"status"`
	CountryCode string               `json:"country_code"`
	Country     string               `json:"country"`
	Countries   []store.JobCountry   `json:"countries"`
	Outlet      *outlets.Outlet      `json:"outlet"`
	Source      mapSource            `json:"source"`
	Techniques  []store.MapTechnique `json:"techniques"`
	Verdict     *string              `json:"verdict"`
	Snippet     string               `json:"snippet"`
}

const snippetRunes = 200

// snippet collapses every whitespace run to one space, trims it and cuts it to 200 code points, then adds U+2026.
func snippet(s string) string {
	var b strings.Builder
	n, space := 0, false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = n > 0
			continue
		}
		if space {
			if n == snippetRunes {
				return b.String() + "…"
			}
			b.WriteByte(' ')
			n, space = n+1, false
		}
		if n == snippetRunes {
			return b.String() + "…"
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// toMapAnalysis is the item of the code's list (upper case, one of the job's map countries).
func toMapAnalysis(it store.MapItem, code string) mapAnalysis {
	c, _ := it.Values.Country(code)
	m := mapAnalysis{JobID: it.Job.ID, CreatedAt: it.Job.CreatedAt, FinishedAt: it.Job.FinishedAt, Status: it.Job.Status,
		CountryCode: code, Country: c.Country, Countries: it.Values.Countries, Outlet: it.Values.Outlet, Techniques: it.Values.Techniques}
	if in := it.Job.Input; in != nil {
		m.Source.URL, m.Snippet = in.Source.URL, snippet(in.Content)
		if in.Source.Title != "" {
			m.Source.Title = &in.Source.Title
		}
	}
	var fin struct {
		Verdict string `json:"verdict"`
	}
	if json.Unmarshal(it.Job.Finalization.Result, &fin) == nil && fin.Verdict != "" {
		m.Verdict = &fin.Verdict
	}
	return m
}

func (a *api) mapAnalyses(w http.ResponseWriter, r *http.Request) {
	bad := func(msg string) { WriteError(w, http.StatusBadRequest, "invalid_request", msg, nil) }
	code := r.PathValue("country_code")
	if len(code) != 2 || !isASCIILetter(code[0]) || !isASCIILetter(code[1]) {
		bad("country_code must be two ASCII letters")
		return
	}
	q := r.URL.Query()
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			bad("limit must be an integer between 1 and 100")
			return
		}
		limit = n
	}
	var after *store.Cursor
	if c := q.Get("cursor"); c != "" {
		at, id, err := decodeCursor(c)
		if err != nil {
			bad("invalid cursor")
			return
		}
		after = &store.Cursor{CreatedAt: at, ID: id}
	}
	code = strings.ToUpper(code)
	its, err := a.svc.MapAnalyses(code, after, limit+1) // one more than the page tells whether there is a next
	if err != nil {
		a.internal(w, "map analyses", err)
		return
	}
	var next *string
	if len(its) > limit {
		its = its[:limit]
		next = new(string)
		*next = encodeCursor(its[limit-1].Job)
	}
	items := make([]mapAnalysis, 0, len(its))
	for _, it := range its {
		items = append(items, toMapAnalysis(it, code))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
