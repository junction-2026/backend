package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
)

func serveOn127(t *testing.T, h http.Handler) (port string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, _ = net.SplitHostPort(l.Addr().String())
	return port
}

func TestHealthcheck(t *testing.T) {
	good := serveOn127(t, ss.NewServer(ss.Config{Token: "t"}, ss.NewRegistry(), nil))
	assert.Equal(t, 0, runHealthcheck("0.0.0.0:"+good))
	assert.Equal(t, 0, runHealthcheck(":"+good))

	bad := serveOn127(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	assert.Equal(t, 1, runHealthcheck("0.0.0.0:"+bad))

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	_, closed, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	assert.Equal(t, 1, runHealthcheck("0.0.0.0:"+closed))
}

func TestRegisterPlatformsInstallsBlueskyAndMastodon(t *testing.T) {
	ss.Default = ss.NewRegistry()
	registerPlatforms(ss.Config{Token: "t", Timeout: 1})
	for _, p := range []string{ss.Bluesky, ss.Mastodon} {
		_, ok := ss.Default.Lookup(p)
		assert.True(t, ok, p)
	}
	for _, p := range []string{ss.Instagram, ss.Threads, ss.Facebook, ss.Twitter} {
		_, ok := ss.Default.Lookup(p)
		assert.False(t, ok, p)
	}
}
