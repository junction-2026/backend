package risk

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func succeeded(id string, result any) job.AnalyzerRun {
	b, _ := json.Marshal(result)
	return job.AnalyzerRun{ID: id, Status: job.RunSucceeded, Result: b}
}

func failed(id string) job.AnalyzerRun {
	return job.AnalyzerRun{ID: id, Status: job.RunFailed, Error: &job.RunError{Code: job.CodeAdapterError}}
}

// techRun builds a techniques result with one ModelResult per model name in order; scores overrides label scores.
func techRun(words int, scores map[string]float64, models ...string) job.AnalyzerRun {
	var rs []map[string]any
	for _, m := range models {
		var sc []map[string]any
		for _, n := range registry.TechniqueNames {
			sc = append(sc, map[string]any{"label": n, "display": adapter.TechniqueDisplay(n), "score": scores[n], "known": true})
		}
		rs = append(rs, map[string]any{"model": m, "cutoff": 0.5, "scores": sc})
	}
	return succeeded("techniques", map[string]any{"words": words, "results": rs})
}

func propRun(label string, score float64) job.AnalyzerRun {
	return succeeded("propaganda_detection", map[string]any{"result": []map[string]any{{"label": label, "score": score}}})
}

// three is the spec's three-technique example: intensity 0.424.
var three = map[string]float64{"Appeal_to_fear-prejudice": 0.9, "Loaded_Language": 0.7, "Doubt": 0.35}

func plain(runs ...job.AnalyzerRun) *job.Job {
	return &job.Job{Input: &job.Input{Content: "x"}, Analyzers: runs}
}

func withAuthor(j *job.Job, runs ...job.AnalyzerRun) *job.Job {
	j.Input.Source.Author = "someone"
	j.Analyzers = append(j.Analyzers, runs...)
	return j
}

func botMapped() Settings {
	s := Default()
	s.BotMapping = BotMapping{AuthorScore: "author.score", AuthorPosts: "author.posts", SingleTextScore: "text.score"}
	return s
}

func bot(author float64, posts int, single float64) job.AnalyzerRun {
	return succeeded("bot_detection", map[string]any{"author": map[string]any{"score": author, "posts": posts}, "text": map[string]any{"score": single}})
}

func ids(s Score) (out []string) {
	for _, c := range s.Caveats {
		out = append(out, c.ID)
	}
	return
}

func near(t *testing.T, want float64, got *float64) {
	t.Helper()
	require.NotNil(t, got)
	require.InDelta(t, want, *got, 5e-5)
}

func TestIntensityThreeTechniques(t *testing.T) {
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8)), Default())
	in := s.Dimensions.Intensity
	require.Equal(t, 3, in.DetectedCount)
	near(t, 0.5, in.Density)
	near(t, 1.74, in.WeightedSum)
	near(t, 0.348, in.WeightedIntensity)
	near(t, 0.424, in.Score)
	require.Equal(t, "nci-technique", *in.Model)
	require.False(t, in.ShortText)
	require.Equal(t, "normal", *in.Confidence)
	// ordering by probability and the RiskTechnique members
	require.Equal(t, []string{"Appeal_to_fear-prejudice", "Loaded_Language", "Doubt"}, []string{s.Techniques[0].Label, s.Techniques[1].Label, s.Techniques[2].Label})
	require.Equal(t, "high", s.Techniques[1].Impact)
	require.Equal(t, 1.0, s.Techniques[1].Weight)
	require.Equal(t, "lower", s.Techniques[2].Impact)
	require.InDelta(t, 0.14, s.Techniques[2].Contribution, 1e-12)
}

// The spec's "six high-impact techniques at 0.8" needs six techniques of weight 1; only four known ones are high, so the
// medium weight is raised to 1 to exercise the same arithmetic.
func TestIntensityPlausibleMaximum(t *testing.T) {
	sc := map[string]float64{}
	for _, n := range []string{"Appeal_to_fear-prejudice", "Loaded_Language", "Reductio_ad_hitlerum", "Black-and-White_Fallacy", "Name_Calling,Labeling", "Exaggeration,Minimisation"} {
		sc[n] = 0.8
	}
	s := Default()
	s.ImpactWeights.Medium = 1
	in := Compute(plain(techRun(300, sc, "nci-technique")), s).Dimensions.Intensity
	near(t, 0.96, in.WeightedIntensity)
	near(t, 0.98, in.Score)
}

func TestThresholdIsStrictAndIndependentOfCutoff(t *testing.T) {
	s := Compute(plain(techRun(300, map[string]float64{"Doubt": 0.3, "Loaded_Language": 0.4}, "nci-technique")), Default())
	require.Len(t, s.Techniques, 1)
	require.Equal(t, "Loaded_Language", s.Techniques[0].Label) // 0.4 is below the 0.5 cutoff of the result but counts
	require.Equal(t, 1, s.Dimensions.Intensity.DetectedCount)
}

func TestNoTechniqueCountsIsAvailableZero(t *testing.T) {
	s := Compute(plain(techRun(300, nil, "nci-technique"), propRun("PROPAGANDA", 0)), Default())
	in := s.Dimensions.Intensity
	require.True(t, in.Available)
	near(t, 0, in.Score)
	require.Empty(t, s.Techniques)
	require.NotNil(t, s.Techniques)
	require.Equal(t, StatusScored, s.Status)
	require.Len(t, s.Drivers, 1)
	require.Equal(t, "none", s.Drivers[0].Kind)
	require.Nil(t, s.Drivers[0].Dimension)
	require.Nil(t, s.Drivers[0].Technique)
}

func TestModelChoice(t *testing.T) {
	both := techRun(60, three, "nci-technique", "nci-social")
	in := Compute(plain(both), Default()).Dimensions.Intensity
	require.Equal(t, "nci-social", *in.Model)
	require.True(t, in.ShortText)
	require.Equal(t, 60, *in.Words)

	in = Compute(plain(techRun(300, three, "nci-technique", "nci-social")), Default()).Dimensions.Intensity
	require.Equal(t, "nci-technique", *in.Model)
	require.False(t, in.ShortText)

	in = Compute(plain(techRun(300, three, "nci-social")), Default()).Dimensions.Intensity
	require.Equal(t, "nci-social", *in.Model, "falls back to the only model")

	in = Compute(plain(techRun(30, three, "nci-social")), Default()).Dimensions.Intensity
	require.Equal(t, 30, *in.Words)
	require.True(t, in.ShortText)
}

func TestIntensityUnavailable(t *testing.T) {
	for name, run := range map[string]job.AnalyzerRun{
		"failed":    failed("techniques"),
		"no scores": succeeded("techniques", map[string]any{"words": 5, "detected": []any{}}),
	} {
		t.Run(name, func(t *testing.T) {
			s := Compute(plain(run, propRun("PROPAGANDA", 0.8)), Default())
			in := s.Dimensions.Intensity
			require.False(t, in.Available)
			require.Nil(t, in.Score)
			require.Equal(t, 0.0, in.Weight)
			require.Nil(t, in.Confidence)
			require.Empty(t, s.Techniques)
			want := DimAnalyzerUnavailable
			if name == "no scores" {
				want = DimNoScores
			}
			require.Equal(t, want, *in.Reason)
			require.Equal(t, StatusScored, s.Status)
			require.Contains(t, ids(s), CaveatSignalMissing)
		})
	}
	// missing run altogether
	require.False(t, Compute(plain(propRun("PROPAGANDA", 0.8)), Default()).Dimensions.Intensity.Available)
}

func TestConfirmation(t *testing.T) {
	c := Compute(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8)), Default()).Dimensions.Confirmation
	near(t, 0.8, c.Score)
	require.True(t, c.Available)
	require.Equal(t, "normal", *c.Confidence)
	require.Nil(t, c.Reason)
	require.Equal(t, "propaganda_detection", *c.Source)
	require.False(t, c.WeakSignal)

	c = Compute(plain(techRun(300, three, "nci-technique"), propRun("NO_PROPAGANDA", 0.9)), Default()).Dimensions.Confirmation
	near(t, 0.1, c.Score)
	require.Equal(t, "NO_PROPAGANDA", *c.Label)
	require.Equal(t, 0.9, *c.ModelScore)
	require.True(t, c.WeakSignal)

	// first PROPAGANDA element wins over NO_PROPAGANDA
	run := succeeded("propaganda_detection", map[string]any{"result": []map[string]any{{"label": "NO_PROPAGANDA", "score": 0.9}, {"label": "PROPAGANDA", "score": 0.4}}})
	near(t, 0.4, Compute(plain(run), Default()).Dimensions.Confirmation.Score)

	for _, r := range []job.AnalyzerRun{failed("propaganda_detection"), succeeded("propaganda_detection", map[string]any{"result": []map[string]any{{"label": "OTHER", "score": 0.5}}})} {
		c = Compute(plain(techRun(300, three, "nci-technique"), r), Default()).Dimensions.Confirmation
		require.False(t, c.Available)
		require.Equal(t, DimAnalyzerUnavailable, *c.Reason)
		require.Nil(t, c.Label)
	}
}

func TestWithoutAmplification(t *testing.T) {
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8)), Default())
	d := s.Dimensions
	require.InDelta(t, 0.55, d.Intensity.Weight, 1e-12)
	require.InDelta(t, 0.45, d.Confirmation.Weight, 1e-12)
	near(t, 0.5932, s.RawScore)
	near(t, 0.5932, s.Score)
	require.Equal(t, "high", *s.Band)
	require.False(t, s.Dampened)
	require.Equal(t, StatusScored, s.Status)
	require.Nil(t, s.Reason)
	// only amplification unavailable, reason no_author_context for a plain text job
	require.Equal(t, DimNoAuthorContext, *d.Amplification.Reason)
	require.Nil(t, d.Amplification.Basis)
	require.Nil(t, d.Amplification.AuthorPosts)
	require.Equal(t, 0.0, d.Amplification.Contribution)
	require.Equal(t, []string{CaveatContentNotReader, CaveatLanguage, CaveatAmplificationMissing}, ids(s)) // no translation run: language unknown
}

func TestAllThreeDimensions(t *testing.T) {
	j := withAuthor(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8), bot(0.9, 120, 0.6)))
	s := Compute(j, botMapped())
	d := s.Dimensions
	require.InDelta(t, 0.4, d.Intensity.Weight, 1e-12)
	require.InDelta(t, 0.3, d.Confirmation.Weight, 1e-12)
	require.InDelta(t, 0.3, d.Amplification.Weight, 1e-12)
	near(t, 0.6796, s.RawScore)
	require.Equal(t, "high", *s.Band)
	near(t, 0.9, d.Amplification.Score)
	require.Equal(t, "author", *d.Amplification.Basis)
	require.Equal(t, 120, *d.Amplification.AuthorPosts)
	require.Equal(t, "normal", *d.Amplification.Confidence)
	require.Equal(t, "bot_detection", *d.Amplification.Source)
	// drivers: amplification 0.27, confirmation 0.24 (intensity 0.1696 is the third)
	require.Equal(t, "amplification", s.Drivers[0].Kind)
	require.Equal(t, "propaganda_pattern", s.Drivers[1].Kind)
	require.Len(t, s.Drivers, 2)
}

func TestTooFewPosts(t *testing.T) {
	j := withAuthor(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8), bot(0.9, 12, 0.6)))
	a := Compute(j, botMapped()).Dimensions.Amplification
	near(t, 0.6, a.Score)
	require.Equal(t, "single_text", *a.Basis)
	require.Equal(t, 12, *a.AuthorPosts)
	require.Equal(t, "low", *a.Confidence)

	// no single-text score either
	run := succeeded("bot_detection", map[string]any{"author": map[string]any{"score": 0.9, "posts": 12}})
	a = Compute(withAuthor(plain(techRun(300, three, "nci-technique"), run)), botMapped()).Dimensions.Amplification
	require.False(t, a.Available)
	require.Equal(t, DimNoBotScore, *a.Reason)
}

func TestAmplificationUnavailable(t *testing.T) {
	base := []job.AnalyzerRun{techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8)}
	// bot detector failed / absent with author context
	require.Equal(t, DimAnalyzerUnavailable, *Compute(withAuthor(plain(append(base, failed("bot_detection"))...)), botMapped()).Dimensions.Amplification.Reason)
	require.Equal(t, DimAnalyzerUnavailable, *Compute(withAuthor(plain(base...)), botMapped()).Dimensions.Amplification.Reason)
	// no author context wins over a succeeded bot detector
	require.Equal(t, DimNoAuthorContext, *Compute(plain(append(base, bot(0.9, 120, 0.6))...), botMapped()).Dimensions.Amplification.Reason)
	// blank author is no context
	j := plain(append(base, bot(0.9, 120, 0.6))...)
	j.Input.Source.Author = "  "
	require.Equal(t, DimNoAuthorContext, *Compute(j, botMapped()).Dimensions.Amplification.Reason)
	// a succeeded scrape is author context even without a source author
	j.Scrape = &job.Scrape{Status: job.RunSucceeded}
	require.True(t, Compute(j, botMapped()).Dimensions.Amplification.Available)
	j.Scrape = &job.Scrape{Status: job.RunFailed}
	require.False(t, Compute(j, botMapped()).Dimensions.Amplification.Available)
	// no mapping configured
	require.Equal(t, DimNoBotScore, *Compute(withAuthor(plain(append(base, bot(0.9, 120, 0.6))...)), Default()).Dimensions.Amplification.Reason)
}

func TestMappingPaths(t *testing.T) {
	doc := map[string]any{"a": []any{map[string]any{"s": 0.7}}, "n": 12.5, "big": 1.5, "posts": 7.0}
	b, _ := json.Marshal(doc)
	var v any
	require.NoError(t, json.Unmarshal(b, &v))
	require.InDelta(t, 0.7, *mappedScore(v, "a.0.s"), 1e-12)
	require.Nil(t, mappedScore(v, "a.1.s"))
	require.Nil(t, mappedScore(v, "a.x.s"))
	require.Nil(t, mappedScore(v, "big"), "out of range")
	require.Nil(t, mappedScore(v, ""))
	require.Nil(t, mappedScore(v, "n.x"))
	require.Nil(t, mappedCount(v, "n"), "not an integer")
	require.Equal(t, 7, *mappedCount(v, "posts"))
}

func TestWeightsRenormalised(t *testing.T) {
	j := withAuthor(plain(techRun(300, three, "nci-technique"), failed("propaganda_detection"), bot(0.9, 120, 0.6)))
	s := Compute(j, botMapped())
	require.InDelta(t, 0.5714, s.Dimensions.Intensity.Weight, 5e-5)
	require.InDelta(t, 0.4286, s.Dimensions.Amplification.Weight, 5e-5)
	near(t, 0.6280, s.RawScore)
	require.Contains(t, ids(s), CaveatSignalMissing)

	// only confirmation, no amplification: weight 1
	s = Compute(plain(failed("techniques"), propRun("PROPAGANDA", 0.8)), Default())
	require.InDelta(t, 1, s.Dimensions.Confirmation.Weight, 1e-12)
	near(t, 0.8, s.Score)
}

func TestNoContentSignals(t *testing.T) {
	for name, j := range map[string]*job.Job{
		"both failed":      plain(failed("techniques"), failed("propaganda_detection")),
		"only amp":         withAuthor(plain(failed("techniques"), failed("propaganda_detection"), bot(0.9, 120, 0.6))),
		"nothing at all":   plain(),
		"skipped analyzer": plain(job.AnalyzerRun{ID: "techniques", Status: job.RunSkipped}),
	} {
		t.Run(name, func(t *testing.T) {
			s := Compute(j, botMapped())
			require.Equal(t, StatusUnavailable, s.Status)
			require.Equal(t, ReasonNoContentSignals, *s.Reason)
			require.Nil(t, s.Score)
			require.Nil(t, s.RawScore)
			require.Nil(t, s.Band)
			require.Equal(t, []Driver{}, s.Drivers)
			require.Equal(t, CaveatContentNotReader, s.Caveats[0].ID)
			require.NotEmpty(t, s.Summary)
			require.Equal(t, 1, s.Version)
			require.Equal(t, 0.0, s.Dimensions.Amplification.Contribution)
		})
	}
}

func TestDampening(t *testing.T) {
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("NO_PROPAGANDA", 0.9)), Default())
	near(t, 0.2782, s.RawScore)
	near(t, 0.1669, s.Score)
	require.True(t, s.Dampened)
	require.Equal(t, "low", *s.Band)
	require.Contains(t, ids(s), CaveatWeakSignal)
}

func TestBandBoundaries(t *testing.T) {
	// intensity 0 and confirmation p give score 0.45p with the defaults; pick p so the score lands on a boundary.
	s := Default()
	s.WeightsWithoutAmp = Weights2{Intensity: 0.5, Confirmation: 0.5}
	for score, want := range map[float64]string{0.5: "high", 0.49: "moderate", 0.25: "moderate", 0.24: "low", 0.75: "critical", 0: "low", 1: "critical"} {
		// score = 0.5*0 + 0.5*p => p = 2*score; p > 1 is impossible, so use intensity too for score 1
		j := plain(techRun(300, nil, "nci-technique"), propRun("PROPAGANDA", 2*score))
		if 2*score > 1 {
			j = plain(failed("techniques"), propRun("PROPAGANDA", score))
		}
		r := Compute(j, s)
		if 2*score > 1 { // confirmation alone carries weight 1
			require.Equal(t, want, *r.Band, fmt.Sprint(score))
			continue
		}
		require.InDelta(t, score, *r.Score, 1e-12)
		require.Equal(t, want, *r.Band, fmt.Sprint(score))
	}
}

func TestDriversAndTechniqueName(t *testing.T) {
	// intensity top: technique with the largest contribution
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("NO_PROPAGANDA", 0.9)), Default())
	require.Equal(t, "technique", s.Drivers[0].Kind)
	require.Equal(t, "intensity", *s.Drivers[0].Dimension)
	require.Equal(t, "Appeal_to_fear-prejudice", *s.Drivers[0].Technique)
	require.Contains(t, s.Drivers[0].Text, "Appeal to fear or prejudice")
	require.Len(t, s.Drivers, 1, "the second contribution is below driver_min_contribution")
	require.Contains(t, s.Summary, "Appeal to fear or prejudice")
	require.True(t, strings.HasPrefix(s.Summary, bandSummary["low"]))

	// the second driver needs driver_min_contribution
	s = Compute(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8)), Default())
	require.Len(t, s.Drivers, 2)
	require.Equal(t, "propaganda_pattern", s.Drivers[0].Kind) // 0.45*0.8 = 0.36 beats 0.55*0.424 = 0.2332
	require.Equal(t, "technique", s.Drivers[1].Kind)
}

func TestDeterminismAndIndependence(t *testing.T) {
	mk := func(extra ...job.AnalyzerRun) *job.Job {
		return withAuthor(plain(append([]job.AnalyzerRun{techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8), bot(0.9, 120, 0.6)}, extra...)...))
	}
	a, b := Compute(mk(), botMapped()), Compute(mk(succeeded("fact_check", map[string]any{"x": 1})), botMapped())
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	require.Equal(t, string(ja), string(jb), "other analyzers do not count")
	require.Equal(t, a, Compute(mk(), botMapped()))
}

func TestCaveatOrder(t *testing.T) {
	en := func(l string) job.AnalyzerRun {
		return succeeded("translation", map[string]any{"source_language": l, "translated_text": "t"})
	}
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("PROPAGANDA", 0.8), en("en")), Default())
	require.Equal(t, []string{CaveatContentNotReader, CaveatAmplificationMissing}, ids(s))

	j := withAuthor(plain(techRun(30, three, "nci-social"), propRun("PROPAGANDA", 0.8), en("de"), bot(0.9, 12, 0.6)))
	s = Compute(j, botMapped())
	require.Equal(t, []string{CaveatContentNotReader, CaveatLanguage, CaveatShortText, CaveatBotNotIntent, CaveatAmplificationLowConfidence}, ids(s))
}

func TestRiskConfigEchoed(t *testing.T) {
	st := Default()
	st.DampeningFactor = 0.5
	s := Compute(plain(techRun(300, three, "nci-technique"), propRun("NO_PROPAGANDA", 0.9)), st)
	require.Equal(t, 0.5, s.Config.DampeningFactor)
	b, _ := json.Marshal(s)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	cfg := m["config"].(map[string]any)
	require.Equal(t, 0.3, cfg["detection_threshold"])
	require.Equal(t, []any{map[string]any{"band": "low", "min": 0.0}, map[string]any{"band": "moderate", "min": 0.25}, map[string]any{"band": "high", "min": 0.5}, map[string]any{"band": "critical", "min": 0.75}}, cfg["bands"])
	require.NotContains(t, cfg, "bot_detection")
	for _, k := range []string{"version", "status", "reason", "score", "raw_score", "band", "dampened", "summary", "dimensions", "techniques", "drivers", "caveats", "config"} {
		require.Contains(t, m, k)
	}
}
