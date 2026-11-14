package web

import (
	"sync"
	"time"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

const maxCacheEntries = 500

type entry struct {
	res     ss.Result
	page    ss.Page
	stored  time.Time
	expires time.Time
}

// cache keeps successful page results in memory; the oldest entry goes first when it is full.
// shortcut: no single-flight, two concurrent scrapes of one URL both call the provider; upgrade if duplicates cost too much.
type cache struct {
	mu  sync.Mutex
	m   map[string]entry
	ttl time.Duration
	now func() time.Time
}

func newCache(ttl time.Duration, now func() time.Time) *cache {
	return &cache{m: map[string]entry{}, ttl: ttl, now: now}
}

// get returns a copy of the cached result marked cached with no provider requests.
func (c *cache) get(key string) (*ss.Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || !c.now().Before(e.expires) {
		delete(c.m, key)
		return nil, false
	}
	res, page := e.res, e.page
	page.Cached, page.OxylabsRequests = true, 0
	res.Page = &page
	return &res, true
}

func (c *cache) put(key string, res *ss.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.m[key]; !ok && len(c.m) >= maxCacheEntries {
		var oldest string
		var at time.Time
		for k, e := range c.m {
			if now.After(e.expires) { // expired entries go first
				delete(c.m, k)
				continue
			}
			if oldest == "" || e.stored.Before(at) {
				oldest, at = k, e.stored
			}
		}
		if len(c.m) >= maxCacheEntries {
			delete(c.m, oldest)
		}
	}
	e := entry{res: *res, page: *res.Page, stored: now, expires: now.Add(c.ttl)}
	e.res.Page = nil
	c.m[key] = e
}
