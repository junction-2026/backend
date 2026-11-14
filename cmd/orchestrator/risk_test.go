package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/risk"
)

func TestRiskScoreConfig(t *testing.T) {
	get := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	c, err := loadConfig(get(nil))
	require.NoError(t, err)
	require.Equal(t, risk.Default(), c.Engine.Risk, "the defaults when RISK_SCORE_CONFIG is unset")

	p := filepath.Join(t.TempDir(), "risk.yaml")
	require.NoError(t, os.WriteFile(p, []byte("dampening_factor: 0.5\n"), 0o600))
	c, err = loadConfig(get(map[string]string{"RISK_SCORE_CONFIG": p}))
	require.NoError(t, err)
	require.Equal(t, 0.5, c.Engine.Risk.DampeningFactor)

	require.NoError(t, os.WriteFile(p, []byte("bands: [{band: low, min: 0.2}]\n"), 0o600))
	_, err = loadConfig(get(map[string]string{"RISK_SCORE_CONFIG": p}))
	require.ErrorContains(t, err, "bands")
	_, err = loadConfig(get(map[string]string{"RISK_SCORE_CONFIG": p + ".missing"}))
	require.ErrorContains(t, err, "RISK_SCORE_CONFIG")
}
