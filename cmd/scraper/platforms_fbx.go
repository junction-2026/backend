package main

import (
	"net/http"
	"os"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/facebook"
	"github.com/junction-2026/backend/internal/socialscraper/x"
)

func init() {
	platformInits = append(platformInits, func(_ ss.Config, hc *http.Client) {
		ss.Register(ss.Facebook, facebook.New())
		ss.Register(ss.Twitter, x.New(hc, os.Getenv))
	})
}
