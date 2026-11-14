package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

func TestRegisterPlatformsInstallsBlueskyMastodonInstagramAndThreads(t *testing.T) {
	ss.Default = ss.NewRegistry()
	registerPlatforms(ss.Config{Token: "t", Timeout: 1})
	for _, p := range []string{ss.Bluesky, ss.Mastodon, ss.Instagram, ss.Threads} {
		_, ok := ss.Default.Lookup(p)
		assert.True(t, ok, p)
	}
	for _, p := range []string{ss.Facebook, ss.Twitter} {
		_, ok := ss.Default.Lookup(p)
		assert.False(t, ok, p)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func start(t *testing.T, env map[string]string) (stop func() (int, string)) {
	t.Helper()
	ss.Default = ss.NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	var out lockedBuf
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, func(k string) string { return env[k] }, &out) }()
	var once sync.Once
	var code int
	stop = func() (int, string) {
		once.Do(func() {
			cancel()
			select {
			case code = <-done:
			case <-time.After(10 * time.Second):
				t.Error("the scraper did not stop within 10 s")
				code = -1
			}
		})
		return code, out.String()
	}
	t.Cleanup(func() { stop() })
	return stop
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	var lastErr error
	for range 100 {
		resp, err := http.Get(url)
		if err == nil {
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, string(b)
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no answer from %s: %v", url, lastErr)
	return 0, ""
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().String()
}

func TestDefaultAddressServesHealthAndSIGTERMExitsZero(t *testing.T) {
	if l, err := net.Listen("tcp", "127.0.0.1:8003"); err != nil {
		t.Skip("port 8003 is in use on this machine")
	} else {
		l.Close()
	}
	stop := start(t, map[string]string{"SCRAPER_TOKEN": "tok"}) // no SCRAPER_LISTEN_ADDR
	status, body := get(t, "http://127.0.0.1:8003/health")
	assert.Equal(t, 200, status)
	assert.JSONEq(t, `{"status":"ok"}`, body)
	begin := time.Now()
	code, _ := stop()
	assert.Equal(t, 0, code)
	assert.Less(t, time.Since(begin), 10*time.Second)
}

func TestMissingTokenExitsNonZeroNamingTheVariable(t *testing.T) {
	var out bytes.Buffer
	addr := freeAddr(t)
	code := run(context.Background(), nil, func(k string) string { return map[string]string{"SCRAPER_LISTEN_ADDR": addr}[k] }, &out)
	assert.NotZero(t, code)
	assert.Contains(t, out.String(), "SCRAPER_TOKEN")
	_, err := http.Get("http://" + addr + "/health") // nothing is served
	assert.Error(t, err)
}

func TestPlatformFileRegistrationReachesItsModule(t *testing.T) {
	saved := platformInits
	t.Cleanup(func() { platformInits = saved })
	called := make(chan ss.Target, 1)
	// what platforms_meta.go / platforms_fbx.go do, without touching platforms.go
	platformInits = append(platformInits, func(ss.Config, *http.Client) {
		ss.Register(ss.Instagram, fakeModule(func(_ context.Context, tg ss.Target, _ ss.Request) (*ss.Result, error) {
			called <- tg
			return &ss.Result{Platform: tg.Platform, URLKind: tg.URLKind, Username: "alice"}, nil
		}))
	})
	addr := freeAddr(t)
	start(t, map[string]string{"SCRAPER_TOKEN": "tok", "SCRAPER_LISTEN_ADDR": addr})
	get(t, "http://"+addr+"/health")

	req, _ := http.NewRequest("POST", "http://"+addr+"/scrape", strings.NewReader(`{"url":"https://www.instagram.com/alice"}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, map[string]string{"username": "alice"}, (<-called).IDs)
}

type fakeModule func(context.Context, ss.Target, ss.Request) (*ss.Result, error)

func (f fakeModule) Scrape(ctx context.Context, t ss.Target, r ss.Request) (*ss.Result, error) {
	return f(ctx, t, r)
}

func TestBinaryExitsZeroOnSIGTERM(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "scraper")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, string(out))
	addr := freeAddr(t)
	cmd := exec.Command(bin)
	cmd.Env = []string{"SCRAPER_TOKEN=tok", "SCRAPER_LISTEN_ADDR=" + addr}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	status, _ := get(t, "http://"+addr+"/health")
	assert.Equal(t, 200, status)

	require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		assert.NoError(t, err) // exit status 0
	case <-time.After(10 * time.Second):
		t.Fatal("no exit within 10 s of SIGTERM")
	}
}
