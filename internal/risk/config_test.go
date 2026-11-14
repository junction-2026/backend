package risk

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func load(t *testing.T, yaml string) (Settings, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "risk.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	return Load(p)
}

func TestLoadDefaultsAndPartial(t *testing.T) {
	s, err := Load("")
	require.NoError(t, err)
	require.Equal(t, Default(), s)

	s, err = load(t, "dampening_factor: 0.5\nbot_detection:\n  author_score: a.b\n  author_posts: a.n\n")
	require.NoError(t, err)
	want := Default()
	want.DampeningFactor = 0.5
	want.BotMapping = BotMapping{AuthorScore: "a.b", AuthorPosts: "a.n"}
	require.Equal(t, want, s)

	s, err = load(t, "") // an empty file is the defaults
	require.NoError(t, err)
	require.Equal(t, Default(), s)

	s, err = load(t, "impact_weights: {high: 2}\nbands:\n  - {band: ok, min: 0}\n  - {band: bad, min: 0.6}\n")
	require.NoError(t, err)
	require.Equal(t, 2.0, s.ImpactWeights.High)
	require.Equal(t, 0.7, s.ImpactWeights.Medium, "unset members keep their default")
	require.Len(t, s.Bands, 2)
}

func TestLoadRejectsInvalid(t *testing.T) {
	for name, c := range map[string]struct{ yaml, member string }{
		"negative impact":     {"impact_weights: {lower: -0.1}", "impact_weights.lower"},
		"negative weight":     {"weights_without_amplification: {intensity: 1.2, confirmation: -0.2}", "weights_without_amplification"},
		"weights not 1":       {"weights_with_amplification: {intensity: 0.5, confirmation: 0.5, amplification: 0.5}", "weights_with_amplification"},
		"zero intensity":      {"weights_without_amplification: {intensity: 0, confirmation: 1}", "weights_without_amplification"},
		"bands not from 0":    {"bands: [{band: low, min: 0.1}, {band: high, min: 0.5}]", "bands"},
		"bands not ascending": {"bands: [{band: low, min: 0}, {band: mid, min: 0.5}, {band: high, min: 0.5}]", "ascending"},
		"bands above 1":       {"bands: [{band: low, min: 0}, {band: high, min: 1.5}]", "bands[1].min"},
		"bands empty":         {"bands: []", "bands"},
		"threshold above 1":   {"detection_threshold: 1.5", "detection_threshold"},
		"threshold negative":  {"dampening_threshold: -0.2", "dampening_threshold"},
		"factor above 1":      {"dampening_factor: 1.1", "dampening_factor"},
		"reference count 0":   {"reference_technique_count: 0", "reference_technique_count"},
		"reference sum 0":     {"weighted_sum_reference: -1", "weighted_sum_reference"},
		"min posts 0":         {"min_author_posts: 0", "min_author_posts"},
		"unknown member":      {"dampening_factr: 0.5", "dampening_factr"},
		"not yaml":            {"detection_threshold: [", "RISK_SCORE_CONFIG"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := load(t, c.yaml)
			require.ErrorContains(t, err, c.member)
		})
	}
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorContains(t, err, "RISK_SCORE_CONFIG cannot be read")
}

func TestShippedExampleLoads(t *testing.T) {
	s, err := Load("../../config/risk_score/risk_score.yaml")
	require.NoError(t, err)
	require.Equal(t, Default().Config, s.Config, "the example documents the defaults")
}
