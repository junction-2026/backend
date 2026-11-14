package main

import (
	"fmt"

	"github.com/junction-2026/backend/internal/engine"
)

const (
	defaultListenAddr      = ":8080"
	defaultAnalyzersConfig = "/etc/orchestrator/analyzers"
)

type config struct {
	ListenAddr      string
	AnalyzersConfig string
	Engine          engine.Config
}

// loadConfig reads LISTEN_ADDR, ANALYZERS_CONFIG and the engine settings
// (MAX_PARALLEL_ANALYZERS, MAX_PARALLEL_JOBS, JOB_RETENTION).
func loadConfig(getenv func(string) string) (config, error) {
	c := config{ListenAddr: getenv("LISTEN_ADDR"), AnalyzersConfig: getenv("ANALYZERS_CONFIG")}
	if c.ListenAddr == "" {
		c.ListenAddr = defaultListenAddr
	}
	if c.AnalyzersConfig == "" {
		c.AnalyzersConfig = defaultAnalyzersConfig
	}
	var err error
	if c.Engine, err = engine.ConfigFromEnv(getenv); err != nil {
		return c, fmt.Errorf("configuration error: %w", err)
	}
	return c, nil
}
