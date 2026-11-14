package risk

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Statuses, reasons, confidences, bases, impacts, driver kinds and caveat ids of the shared contract.
const (
	StatusScored      = "scored"
	StatusUnavailable = "unavailable"

	ReasonNoContentSignals = "no_content_signals"

	DimAnalyzerUnavailable = "analyzer_unavailable"
	DimNoScores            = "no_scores"
	DimNoAuthorContext     = "no_author_context"
	DimNoBotScore          = "no_bot_score"

	CaveatContentNotReader           = "content_not_reader"
	CaveatLanguage                   = "language"
	CaveatShortText                  = "short_text"
	CaveatBotNotIntent               = "bot_not_intent"
	CaveatAmplificationLowConfidence = "amplification_low_confidence"
	CaveatAmplificationMissing       = "amplification_missing"
	CaveatSignalMissing              = "signal_missing"
	CaveatWeakSignal                 = "weak_signal"

	modelTechnique = "nci-technique"
	modelSocial    = "nci-social"
)

// Dimension holds the RiskDimension members every dimension has.
type Dimension struct {
	Available    bool     `json:"available"`
	Score        *float64 `json:"score"`
	Weight       float64  `json:"weight"`
	Contribution float64  `json:"contribution"`
	Confidence   *string  `json:"confidence"`
	Reason       *string  `json:"reason"`
	Source       *string  `json:"source"`
}

type Intensity struct {
	Dimension
	Model             *string  `json:"model"`
	Words             *int     `json:"words"`
	ShortText         bool     `json:"short_text"`
	DetectedCount     int      `json:"detected_count"`
	Density           *float64 `json:"density"`
	WeightedSum       *float64 `json:"weighted_sum"`
	WeightedIntensity *float64 `json:"weighted_intensity"`
}

type Confirmation struct {
	Dimension
	Label      *string  `json:"label"`
	ModelScore *float64 `json:"model_score"`
	WeakSignal bool     `json:"weak_signal"`
}

type Amplification struct {
	Dimension
	Basis       *string `json:"basis"`
	AuthorPosts *int    `json:"author_posts"`
}

type Dimensions struct {
	Intensity     Intensity     `json:"intensity"`
	Confirmation  Confirmation  `json:"confirmation"`
	Amplification Amplification `json:"amplification"`
}

type Technique struct {
	Label        string  `json:"label"`
	Display      string  `json:"display"`
	Probability  float64 `json:"probability"`
	Impact       string  `json:"impact"`
	Weight       float64 `json:"weight"`
	Contribution float64 `json:"contribution"`
}

type Driver struct {
	Kind         string  `json:"kind"`
	Dimension    *string `json:"dimension"`
	Technique    *string `json:"technique"`
	Contribution float64 `json:"contribution"`
	Text         string  `json:"text"`
}

type Caveat struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Score is the shared RiskScore object.
type Score struct {
	Version    int         `json:"version"`
	Status     string      `json:"status"`
	Reason     *string     `json:"reason"`
	Score      *float64    `json:"score"`
	RawScore   *float64    `json:"raw_score"`
	Band       *string     `json:"band"`
	Dampened   bool        `json:"dampened"`
	Summary    string      `json:"summary"`
	Dimensions Dimensions  `json:"dimensions"`
	Techniques []Technique `json:"techniques"`
	Drivers    []Driver    `json:"drivers"`
	Caveats    []Caveat    `json:"caveats"`
	Config     Config      `json:"config"`
}

var impactGroups = map[string]string{
	"Appeal_to_fear-prejudice": "high", "Loaded_Language": "high", "Reductio_ad_hitlerum": "high", "Black-and-White_Fallacy": "high",
	"Name_Calling,Labeling": "medium", "Exaggeration,Minimisation": "medium", "Causal_Oversimplification": "medium",
	"Thought-terminating_Cliches": "medium", "Bandwagon": "medium", "Slogans": "medium",
	"Doubt": "lower", "Repetition": "lower", "Flag-Waving": "lower", "Straw_Man": "lower", "Red_Herring": "lower",
	"Whataboutism,Straw_Men,Red_Herring": "lower", "Appeal_to_Authority": "lower", "Obfuscation,Intentional_Vagueness,Confusion": "lower",
}

// Compute is a pure function of the job (all analyzers settled) and the settings.
func Compute(j *job.Job, s Settings) Score {
	c := s.Config
	in, techs := intensity(j, c)
	conf := confirmation(j, c)
	amp := amplification(j, c, s.BotMapping)
	out := Score{Version: 1, Dimensions: Dimensions{Intensity: in, Confirmation: conf, Amplification: amp}, Techniques: techs, Drivers: []Driver{}, Config: c}

	if !in.Available && !conf.Available {
		out.Status, out.Reason = StatusUnavailable, job.Str(ReasonNoContentSignals)
		out.Summary = summaryUnavailable
		out.Caveats = caveats(j, &out)
		return out
	}

	// effective weights
	var wi, wc, wa float64
	if amp.Available {
		wi, wc, wa = c.WeightsWithAmplification.Intensity, c.WeightsWithAmplification.Confirmation, c.WeightsWithAmplification.Amplification
	} else {
		wi, wc = c.WeightsWithoutAmp.Intensity, c.WeightsWithoutAmp.Confirmation
	}
	if !in.Available || !conf.Available {
		sum := wa
		if in.Available {
			sum += wi
		}
		if conf.Available {
			sum += wc
		}
		wi, wc, wa = wi/sum, wc/sum, wa/sum
	}
	d := &out.Dimensions
	raw := 0.0
	for _, x := range []struct {
		d *Dimension
		w float64
	}{{&d.Intensity.Dimension, wi}, {&d.Confirmation.Dimension, wc}, {&d.Amplification.Dimension, wa}} {
		if x.d.Available {
			x.d.Weight, x.d.Contribution = x.w, x.w**x.d.Score
			raw += x.d.Contribution
		}
	}
	score := raw
	if conf.WeakSignal {
		score, out.Dampened = raw*c.DampeningFactor, true
	}
	out.Status, out.RawScore, out.Score = StatusScored, &raw, &score
	for i := len(c.Bands) - 1; i >= 0; i-- {
		if c.Bands[i].Min <= score {
			out.Band = &c.Bands[i].Band
			break
		}
	}
	out.Drivers = drivers(&out, c)
	out.Summary = bandSummaryText(*out.Band) + " " + out.Drivers[0].Text
	out.Caveats = caveats(j, &out)
	return out
}

func run(j *job.Job, id string) *job.AnalyzerRun {
	for i := range j.Analyzers {
		if j.Analyzers[i].ID == id && j.Analyzers[i].Status == job.RunSucceeded {
			return &j.Analyzers[i]
		}
	}
	return nil
}

func unavailable(reason string) Dimension { return Dimension{Reason: &reason} }

func available(score float64, confidence, source string) Dimension {
	return Dimension{Available: true, Score: &score, Confidence: &confidence, Source: &source}
}

type techniqueScore struct {
	Label   string  `json:"label"`
	Display string  `json:"display"`
	Score   float64 `json:"score"`
	Known   bool    `json:"known"`
}

func intensity(j *job.Job, c Config) (Intensity, []Technique) {
	techs := []Technique{}
	r := run(j, "techniques")
	if r == nil {
		return Intensity{Dimension: unavailable(DimAnalyzerUnavailable)}, techs
	}
	var res struct {
		Words   *int `json:"words"`
		Results []struct {
			Model  string           `json:"model"`
			Scores []techniqueScore `json:"scores"`
		} `json:"results"`
	}
	if json.Unmarshal(r.Result, &res) != nil || len(res.Results) == 0 {
		return Intensity{Dimension: unavailable(DimNoScores)}, techs
	}
	find := func(model string) int {
		for i := range res.Results {
			if res.Results[i].Model == model {
				return i
			}
		}
		return -1
	}
	social, doc := find(modelSocial), find(modelTechnique)
	pick := social
	if social < 0 || res.Words == nil || *res.Words > c.ShortTextMaxWords {
		if doc >= 0 {
			pick = doc
		}
	}
	if pick < 0 {
		return Intensity{Dimension: unavailable(DimNoScores)}, techs
	}
	model := res.Results[pick].Model
	order := map[string]int{}
	for i, n := range registry.TechniqueNames {
		order[n] = i
	}
	sum := 0.0
	for _, t := range res.Results[pick].Scores {
		group, ok := impactGroups[t.Label]
		if !t.Known || !ok || !(t.Score > c.DetectionThreshold) {
			continue
		}
		w := map[string]float64{"high": c.ImpactWeights.High, "medium": c.ImpactWeights.Medium, "lower": c.ImpactWeights.Lower}[group]
		techs = append(techs, Technique{Label: t.Label, Display: t.Display, Probability: t.Score, Impact: group, Weight: w, Contribution: t.Score * w})
		sum += t.Score * w
	}
	sort.SliceStable(techs, func(a, b int) bool {
		if techs[a].Probability != techs[b].Probability {
			return techs[a].Probability > techs[b].Probability
		}
		return order[techs[a].Label] < order[techs[b].Label]
	})
	density := math.Min(1, float64(len(techs))/c.ReferenceTechniqueCount)
	wi := math.Min(1, sum/c.WeightedSumReference)
	in := Intensity{
		Dimension: available((density+wi)/2, "normal", "techniques"),
		Model:     &model, Words: res.Words, ShortText: model == modelSocial,
		DetectedCount: len(techs), Density: &density, WeightedSum: &sum, WeightedIntensity: &wi,
	}
	return in, techs
}

func confirmation(j *job.Job, c Config) Confirmation {
	r := run(j, "propaganda_detection")
	if r == nil {
		return Confirmation{Dimension: unavailable(DimAnalyzerUnavailable)}
	}
	var res struct {
		Result []struct {
			Label *string  `json:"label"`
			Score *float64 `json:"score"`
		} `json:"result"`
	}
	if json.Unmarshal(r.Result, &res) != nil {
		return Confirmation{Dimension: unavailable(DimAnalyzerUnavailable)}
	}
	pick := func(label string) (float64, bool) {
		for _, e := range res.Result {
			if e.Label != nil && *e.Label == label && e.Score != nil && *e.Score >= 0 && *e.Score <= 1 {
				return *e.Score, true
			}
		}
		return 0, false
	}
	label, score, ok := "PROPAGANDA", 0.0, false
	if score, ok = pick(label); !ok {
		label = "NO_PROPAGANDA"
		if score, ok = pick(label); !ok {
			return Confirmation{Dimension: unavailable(DimAnalyzerUnavailable)}
		}
	}
	p := score
	if label == "NO_PROPAGANDA" {
		p = 1 - score
	}
	return Confirmation{Dimension: available(p, "normal", "propaganda_detection"), Label: &label, ModelScore: &score, WeakSignal: p < c.DampeningThreshold}
}

func amplification(j *job.Job, c Config, m BotMapping) Amplification {
	hasAuthor := (j.Scrape != nil && j.Scrape.Status == job.RunSucceeded) || (j.Input != nil && strings.TrimSpace(j.Input.Source.Author) != "")
	if !hasAuthor {
		return Amplification{Dimension: unavailable(DimNoAuthorContext)}
	}
	r := run(j, "bot_detection")
	if r == nil {
		return Amplification{Dimension: unavailable(DimAnalyzerUnavailable)}
	}
	var doc any
	if json.Unmarshal(r.Result, &doc) != nil {
		doc = nil
	}
	author, single := mappedScore(doc, m.AuthorScore), mappedScore(doc, m.SingleTextScore)
	posts := mappedCount(doc, m.AuthorPosts)
	switch {
	case author != nil && posts != nil && *posts >= c.MinAuthorPosts:
		return Amplification{Dimension: available(*author, "normal", "bot_detection"), Basis: job.Str("author"), AuthorPosts: posts}
	case single != nil:
		return Amplification{Dimension: available(*single, "low", "bot_detection"), Basis: job.Str("single_text"), AuthorPosts: posts}
	}
	return Amplification{Dimension: unavailable(DimNoBotScore), AuthorPosts: posts}
}

// lookup follows a dot-separated path through objects; a numeric segment indexes an array. nil when it does not resolve.
func lookup(doc any, path string) any {
	if path == "" {
		return nil
	}
	for _, seg := range strings.Split(path, ".") {
		switch v := doc.(type) {
		case map[string]any:
			doc = v[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(v) {
				return nil
			}
			doc = v[i]
		default:
			return nil
		}
	}
	return doc
}

func mappedScore(doc any, path string) *float64 {
	if f, ok := lookup(doc, path).(float64); ok && f >= 0 && f <= 1 {
		return &f
	}
	return nil
}

func mappedCount(doc any, path string) *int {
	if f, ok := lookup(doc, path).(float64); ok && f >= 0 && f == math.Trunc(f) && f < 1<<53 {
		return job.Ptr(int(f))
	}
	return nil
}

// drivers lists the top dimension and, when large enough, the second (scored RiskScores only).
func drivers(s *Score, c Config) []Driver {
	d := s.Dimensions
	cand := []Driver{
		{Kind: "technique", Dimension: job.Str("intensity"), Contribution: d.Intensity.Contribution},
		{Kind: "propaganda_pattern", Dimension: job.Str("confirmation"), Contribution: d.Confirmation.Contribution},
		{Kind: "amplification", Dimension: job.Str("amplification"), Contribution: d.Amplification.Contribution},
	}
	var out []Driver
	for _, x := range cand {
		if x.Contribution > 0 { // an unavailable dimension has contribution 0
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Contribution > out[b].Contribution })
	if len(out) == 0 {
		return []Driver{{Kind: "none", Text: driverNone}}
	}
	if len(out) > 1 && out[1].Contribution >= c.DriverMinContribution {
		out = out[:2]
	} else {
		out = out[:1]
	}
	for i := range out {
		switch out[i].Kind {
		case "technique":
			out[i].Text = driverTechniqueNoName
			best := -1
			for k, t := range s.Techniques {
				if best < 0 || t.Contribution > s.Techniques[best].Contribution {
					best = k
				}
			}
			if best >= 0 {
				out[i].Technique, out[i].Text = &s.Techniques[best].Label, driverTechniqueText(s.Techniques[best].Display)
			}
		case "propaganda_pattern":
			out[i].Text = driverPropagandaPattern
		case "amplification":
			out[i].Text = driverAmplification
		}
	}
	return out
}

func caveats(j *job.Job, s *Score) []Caveat {
	d := s.Dimensions
	ids := []string{CaveatContentNotReader}
	add := func(ok bool, id string) {
		if ok {
			ids = append(ids, id)
		}
	}
	add(adapter.JobSourceLanguage(j) != "en", CaveatLanguage)
	add(d.Intensity.ShortText, CaveatShortText)
	amp := d.Amplification
	add(amp.Available, CaveatBotNotIntent)
	add(amp.Available && *amp.Confidence == "low", CaveatAmplificationLowConfidence)
	add(!amp.Available, CaveatAmplificationMissing)
	add(!d.Intensity.Available || !d.Confirmation.Available, CaveatSignalMissing)
	add(s.Dampened, CaveatWeakSignal)
	out := make([]Caveat, len(ids))
	for i, id := range ids {
		out[i] = Caveat{ID: id, Text: caveatTexts[id]}
	}
	return out
}
