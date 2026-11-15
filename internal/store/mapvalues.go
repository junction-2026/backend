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

// MapValues are the values a job contributes to the propaganda map; they are fixed when the job becomes done, except
// that the country association backfill determines the countries again with the fixed outlet and techniques.
type MapValues struct {
	Eligible   bool            // a map analysis: done, public, techniques detected, at least one map country
	Countries  []JobCountry    // the map countries in order (Map country); nil when none
	Outlet     *outlets.Outlet // the map outlet; nil when none
	Techniques []MapTechnique  // the technique set; nil without detected propaganda
}

// JobCountry is one map country of a job, as stored and as served (propaganda-map-api MapCountry).
type JobCountry struct {
	Code    string   `json:"country_code"`
	Country string   `json:"country"`
	Basis   []string `json:"basis"` // "outlet", "content"
}

// Country returns the job's map country with the code, if any.
func (v MapValues) Country(code string) (JobCountry, bool) {
	for _, c := range v.Countries {
		if c.Code == code {
			return c, true
		}
	}
	return JobCountry{}, false
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
// A job that is not done, not public or without detected propaganda yields the zero MapValues; one with detected
// propaganda but no map country keeps its outlet and techniques (not Eligible) for a later backfill.
func DeriveMap(j *job.Job, table *outlets.Table) MapValues {
	return deriveMap(j, func() *outlets.Outlet { return mapOutlet(j, table) })
}

// deriveMap is DeriveMap with the map outlet given (called only for a done public job with detected propaganda).
func deriveMap(j *job.Job, outlet func() *outlets.Outlet) MapValues {
	if !j.Done || !j.Public {
		return MapValues{}
	}
	techniques := DetectedTechniques(j)
	if len(techniques) == 0 {
		return MapValues{}
	}
	v := MapValues{Outlet: outlet(), Techniques: techniques}
	add := func(code, name, basis string) {
		for i := range v.Countries {
			if v.Countries[i].Code == code {
				v.Countries[i].Basis = append(v.Countries[i].Basis, basis)
				return
			}
		}
		v.Countries = append(v.Countries, JobCountry{code, name, []string{basis}})
	}
	if o := v.Outlet; o != nil {
		if code := strings.ToUpper(strings.TrimSpace(o.CountryCode)); len(code) == 2 && isASCIILetter(code[0]) && isASCIILetter(code[1]) {
			add(code, o.Country, "outlet")
		}
	}
	if a := j.CountryAssociation; a != nil && a.Status == job.RunSucceeded {
		for _, c := range a.Countries {
			add(c.CountryCode, c.Country, "content")
		}
	}
	v.Eligible = len(v.Countries) > 0
	return v
}

func isASCIILetter(c byte) bool { return c >= 'A' && c <= 'Z' }

// DetectedTechniques: the distinct known technique names of techniques.result.detected (weak included) in
// registry.TechniqueNames order; nil unless the run succeeded. Non-empty means the job has detected propaganda.
func DetectedTechniques(j *job.Job) []MapTechnique {
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

// PageOutlet is the stored page_outlet of a succeeded source_info run, nil when none.
func PageOutlet(j *job.Job) *outlets.Outlet {
	if run := runByID(j, "source_info"); run != nil && run.Status == job.RunSucceeded {
		var res struct {
			PageOutlet *outlets.Outlet `json:"page_outlet"`
		}
		if json.Unmarshal(run.Result, &res) == nil && res.PageOutlet != nil {
			return res.PageOutlet
		}
	}
	return nil
}

// mapOutlet is PageOutlet, else the outlet-table match of input.source.url. An outlet without country code is
// returned as is.
func mapOutlet(j *job.Job, table *outlets.Table) *outlets.Outlet {
	if o := PageOutlet(j); o != nil {
		return o
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

// add counts one map analysis in each of its countries (Memory store).
func (a *mapAgg) add(k mapKey, v MapValues) {
	for _, mc := range v.Countries {
		c := a.country(mc.Code)
		c.Analyses++
		if n, ok := a.newest[mc.Code]; !ok || k.before(n) {
			a.newest[mc.Code], c.Country = k, mc.Country
		}
		for _, t := range v.Techniques {
			a.counts[mc.Code][t.Name]++
		}
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
