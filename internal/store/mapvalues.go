package store

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
	"github.com/junction-2026/backend/internal/registry"
)

// MapTechnique is one technique of a map analysis, as stored and as served (propaganda-map-api MapTechnique).
type MapTechnique struct {
	Name     string `json:"name"`
	Display  string `json:"display"`
	Strength string `json:"strength,omitempty"` // only when the stored Detection has it
}

// MapValues are the values a job contributes to the propaganda map; they are fixed when the job becomes done.
type MapValues struct {
	Eligible    bool // a map analysis: done, public, techniques detected, map country known
	CountryCode string
	Country     string
	Outlet      *outlets.Outlet
	Techniques  []MapTechnique
}

// MapItem is a map analysis: the job and its map values.
type MapItem struct {
	Job    job.Job
	Values MapValues
}

// TechniqueCount is the number of a country's map analyses that carry a technique.
type TechniqueCount struct {
	Name     string
	Analyses int
}

// MapCountry is the aggregate of the map analyses of one country; Country is that of the newest one.
type MapCountry struct {
	Code       string
	Country    string
	Analyses   int
	Techniques []TechniqueCount // analyses descending, then technique-name order
}

var techniqueOrder = func() map[string]int {
	m := map[string]int{}
	for i, n := range registry.TechniqueNames {
		m[n] = i
	}
	return m
}()

func runByID(j *job.Job, id string) *job.AnalyzerRun {
	for i := range j.Analyzers {
		if j.Analyzers[i].ID == id {
			return &j.Analyzers[i]
		}
	}
	return nil
}

// DeriveMap computes the map values of a job from its stored results and, for the outlet fallback, table (may be nil).
// A job that is not a map analysis yields the zero MapValues.
func DeriveMap(j *job.Job, table *outlets.Table) MapValues {
	if !j.Done || !j.Public {
		return MapValues{}
	}
	techniques := detectedTechniques(j)
	if len(techniques) == 0 {
		return MapValues{}
	}
	outlet := pageOutlet(j, table)
	if outlet == nil || strings.TrimSpace(outlet.CountryCode) == "" {
		return MapValues{}
	}
	return MapValues{Eligible: true, CountryCode: strings.ToUpper(strings.TrimSpace(outlet.CountryCode)), Country: outlet.Country,
		Outlet: outlet, Techniques: techniques}
}

// detectedTechniques: the distinct known technique names of techniques.result.detected (weak included) in
// registry.TechniqueNames order; nil unless the run succeeded.
func detectedTechniques(j *job.Job) []MapTechnique {
	run := runByID(j, "techniques")
	if run == nil || run.Status != job.RunSucceeded {
		return nil
	}
	var res struct {
		Detected []MapTechnique `json:"detected"`
	}
	if json.Unmarshal(run.Result, &res) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []MapTechnique
	for _, d := range res.Detected {
		if _, known := techniqueOrder[d.Name]; !known || seen[d.Name] {
			continue
		}
		seen[d.Name] = true
		if d.Display == "" {
			d.Display = d.Name
		}
		out = append(out, d)
	}
	sort.Slice(out, func(a, b int) bool { return techniqueOrder[out[a].Name] < techniqueOrder[out[b].Name] })
	return out
}

// pageOutlet is the stored page_outlet of a succeeded source_info run, else the outlet-table match of
// input.source.url; never inferred from the TLD, language or text. An outlet without country code is returned as is.
func pageOutlet(j *job.Job, table *outlets.Table) *outlets.Outlet {
	if run := runByID(j, "source_info"); run != nil && run.Status == job.RunSucceeded {
		var res struct {
			PageOutlet *outlets.Outlet `json:"page_outlet"`
		}
		if json.Unmarshal(run.Result, &res) == nil && res.PageOutlet != nil {
			return res.PageOutlet
		}
	}
	if table == nil || j.Input == nil {
		return nil
	}
	host := outlets.Host(j.Input.Source.URL)
	if host == "" {
		return nil
	}
	o, _ := table.Match(host)
	return o
}

// mapAgg collects per-country aggregates for both stores.
type mapAgg struct {
	countries map[string]*MapCountry
	counts    map[string]map[string]int
	newest    map[string]mapKey
}

// mapKey orders map analyses: newest first, ties by id.
type mapKey struct {
	CreatedAt time.Time
	ID        string
}

func (k mapKey) before(o mapKey) bool { // k is listed before o
	if !k.CreatedAt.Equal(o.CreatedAt) {
		return k.CreatedAt.After(o.CreatedAt)
	}
	return k.ID < o.ID
}

func newMapAgg() *mapAgg {
	return &mapAgg{countries: map[string]*MapCountry{}, counts: map[string]map[string]int{}, newest: map[string]mapKey{}}
}

func (a *mapAgg) country(code string) *MapCountry {
	c := a.countries[code]
	if c == nil {
		c = &MapCountry{Code: code}
		a.countries[code] = c
		a.counts[code] = map[string]int{}
	}
	return c
}

// add counts one map analysis (Memory store).
func (a *mapAgg) add(k mapKey, v MapValues) {
	c := a.country(v.CountryCode)
	c.Analyses++
	if n, ok := a.newest[v.CountryCode]; !ok || k.before(n) {
		a.newest[v.CountryCode], c.Country = k, v.Country
	}
	for _, t := range v.Techniques {
		a.counts[v.CountryCode][t.Name]++
	}
}

// result: countries by code, techniques by analyses descending then technique-name order.
func (a *mapAgg) result() []MapCountry {
	out := make([]MapCountry, 0, len(a.countries))
	for code, c := range a.countries {
		for name, n := range a.counts[code] {
			c.Techniques = append(c.Techniques, TechniqueCount{name, n})
		}
		sort.Slice(c.Techniques, func(i, j int) bool {
			x, y := c.Techniques[i], c.Techniques[j]
			if x.Analyses != y.Analyses {
				return x.Analyses > y.Analyses
			}
			return techniqueOrder[x.Name] < techniqueOrder[y.Name]
		})
		if c.Techniques == nil {
			c.Techniques = []TechniqueCount{}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}
