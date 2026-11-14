package socialscraper

import "sync"

// Registry maps a platform to its Module. Platforms without one fail with platform_not_configured.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Module
}

func NewRegistry() *Registry { return &Registry{m: map[string]Module{}} }

// Register installs m for platform (replacing an earlier one).
func (r *Registry) Register(platform string, m Module) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[platform] = m
}

func (r *Registry) Lookup(platform string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.m[platform]
	return m, ok
}
