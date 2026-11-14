package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ss "github.com/junction-2026/backend/internal/socialscraper"
	"github.com/junction-2026/backend/internal/socialscraper/scrapertest"
)

const sentinel = "SENTINEL-sessionid-91c7"

func fixture(t *testing.T, platform, name string) []byte {
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "socialscraper", platform, "testdata", name))
	require.NoError(t, err)
	return b
}

// metaServer is the scraper server with the Instagram and Threads modules over routed fixtures.
type metaServer struct {
	srv  *httptest.Server
	logs *bytes.Buffer
	mu   sync.Mutex
	seen []*http.Request
}

func newMetaServer(t *testing.T, sessionFile string, extra func(path string) (int, []byte)) *metaServer {
	ms := &metaServer{logs: &bytes.Buffer{}}
	ig := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ms.mu.Lock()
		ms.seen = append(ms.seen, r)
		ms.mu.Unlock()
		p := r.URL.Path
		if extra != nil {
			if status, body := extra(p); status != 0 {
				scrapertest.Body(status, body)(w, r)
				return
			}
		}
		name := ""
		switch {
		case strings.HasSuffix(p, "/web_profile_info/"):
			name = "web_profile_info_alice.json"
		case strings.Contains(p, "/feed/user/"):
			name = "feed_alice_page1.json"
		case strings.HasSuffix(p, "/child_comments/"):
			name = "child_comments_9002.json"
		case strings.HasSuffix(p, "/comments/"):
			name = "comments_CpB222.json"
		case strings.HasSuffix(p, "/info/"):
			name = "media_CpB222.json"
		}
		if name == "" {
			t.Errorf("unexpected Instagram request %s", r.URL)
			w.WriteHeader(500)
			return
		}
		scrapertest.Body(200, fixture(t, "instagram", name))(w, r)
	})
	threads := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/@alice" {
			t.Errorf("unexpected Threads request %s", r.URL)
		}
		scrapertest.Body(200, fixture(t, "threads", "profile.html"), "Content-Type", "text/html")(w, r)
	})
	router := scrapertest.New(t).Handle("www.instagram.com", ig).Handle("i.instagram.com", ig).Handle("www.threads.net", threads)
	// an unredacted logger: nothing may leak even without the redactor
	log := slog.New(slog.NewJSONHandler(ms.logs, nil))
	reg := ss.NewRegistry()
	registerMeta(reg, ss.NewClient(5*time.Second, router), sessionFile, log)
	ms.srv = httptest.NewServer(ss.NewServer(ss.Config{Token: "tok", Timeout: 5 * time.Second}, reg, log))
	t.Cleanup(ms.srv.Close)
	return ms
}

func (ms *metaServer) scrape(t *testing.T, url string, extra string) (*http.Response, []byte) {
	req, err := http.NewRequest(http.MethodPost, ms.srv.URL+"/scrape", strings.NewReader(`{"url":"`+url+`"`+extra+`}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req) // httptest server on loopback
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestInstagramReelAndThreadsProfileThroughTheServer(t *testing.T) {
	ms := newMetaServer(t, "", nil)

	resp, body := ms.scrape(t, "https://www.instagram.com/reel/C1a2b3c4d5/", "")
	require.Equal(t, 200, resp.StatusCode, string(body))
	var res ss.Result
	require.NoError(t, json.Unmarshal(body, &res))
	assert.Equal(t, "instagram", res.Platform)
	assert.Equal(t, "post", res.URLKind)
	assert.Equal(t, "alice", res.Username)
	assert.Equal(t, "CpB222", res.Post.ID)
	assert.Equal(t, "comments_unavailable", res.Warnings[0].Code)

	resp, body = ms.scrape(t, "https://www.threads.net/@alice", "")
	require.Equal(t, 200, resp.StatusCode, string(body))
	res = ss.Result{}
	require.NoError(t, json.Unmarshal(body, &res))
	assert.Equal(t, "threads", res.Platform)
	assert.Equal(t, "profile", res.URLKind)
	assert.Equal(t, 4321, *res.Followers)
}

func TestSessionValueAndPathNeverLeaveTheServer(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "ig-session-secret-path.json")
	require.NoError(t, os.WriteFile(session, []byte(`{"sessionid":"`+sentinel+`","csrftoken":"csrf-sentinel-3b","ds_user_id":"42"}`), 0o600))
	broken := filepath.Join(dir, "ig-broken-secret-path.json")
	require.NoError(t, os.WriteFile(broken, []byte("nothing"), 0o600))

	for name, c := range map[string]struct {
		file       string
		wantCookie bool
		extra      func(string) (int, []byte)
	}{
		"session":        {session, true, nil},
		"broken session": {broken, false, nil},
		"upstream echoes": {session, true, func(p string) (int, []byte) { // even an upstream body naming the cookie stays upstream
			if strings.HasSuffix(p, "/comments/") {
				return 500, []byte(sentinel)
			}
			return 0, nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ms := newMetaServer(t, c.file, c.extra)
			resp, body := ms.scrape(t, "https://www.instagram.com/p/CpB222/", `,"max_comments":5`)
			require.Equal(t, 200, resp.StatusCode, string(body))
			var res ss.Result
			require.NoError(t, json.Unmarshal(body, &res))
			if c.wantCookie && name == "session" {
				assert.NotEmpty(t, res.Comments, "comments are read with the session")
			} else {
				assert.Empty(t, res.Comments)
			}
			cookies := 0
			for _, r := range ms.seen {
				if r.Header.Get("Cookie") != "" {
					cookies++
				}
			}
			assert.Equal(t, c.wantCookie, cookies > 0, "cookie sent only with a valid session")

			headers := &bytes.Buffer{}
			require.NoError(t, resp.Header.Write(headers))
			for _, secret := range []string{sentinel, "csrf-sentinel-3b", session, broken, filepath.Base(session), filepath.Base(broken)} {
				assert.NotContains(t, string(body), secret)
				assert.NotContains(t, headers.String(), secret)
				assert.NotContains(t, ms.logs.String(), secret)
			}
		})
	}
}
