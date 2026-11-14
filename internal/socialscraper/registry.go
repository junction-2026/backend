package socialscraper

import "sync"

// Registry maps a platform to its Module. Platforms without one answer 503 platform_not_configured.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Module
}

func NewRegistry() *Registry { return &Registry{m: map[string]Module{}} }

// Default is the registry cmd/scraper fills through Register.
var Default = NewRegistry()

// Register installs m for platform (replacing an earlier one).
func Register(platform string, m Module) { Default.Register(platform, m) }

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
