package main

import (
	"net/http"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/bluesky"
	"github.com/junction-2026/backend/internal/socialscraper/mastodon"
)

// platformInit registers modules once the configuration is known. A platform module in another
// file of this package adds its own from init():
//
//	func init() { platformInits = append(platformInits, func(cfg ss.Config, hc *http.Client) { ss.Register(...) }) }
//
// so platforms.go is the only file that names the modules of the core.
var platformInits []func(cfg ss.Config, hc *http.Client)

func init() {
	platformInits = append(platformInits, func(_ ss.Config, hc *http.Client) {
		ss.Register(ss.Bluesky, bluesky.New(hc))
		ss.Register(ss.Mastodon, mastodon.New(hc))
	})
}

// registerPlatforms fills the registry; hc is bounded by the upstream timeout and caps response bodies.
func registerPlatforms(cfg ss.Config) {
	hc := ss.NewClient(cfg.Timeout, nil)
	for _, f := range platformInits {
		f(cfg, hc)
	}
}
