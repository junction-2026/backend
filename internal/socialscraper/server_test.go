package socialscraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const token = "test-token-123456"

type moduleFunc func(ctx context.Context, t Target, req Request) (*Result, error)

func (f moduleFunc) Scrape(ctx context.Context, t Target, req Request) (*Result, error) {
	return f(ctx, t, req)
}

// okModule answers a minimal profile and records what it was called with.
type okModule struct {
	got Request
	url Target
}

func (m *okModule) Scrape(_ context.Context, t Target, req Request) (*Result, error) {
	m.got, m.url = req, t
	return &Result{Platform: t.Platform, URLKind: t.URLKind, Username: "alice"}, nil
}

func newTestServer(t *testing.T, timeout time.Duration, reg *Registry) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	log := NewLogger(&logs, NewRedactor(env(map[string]string{"SCRAPER_TOKEN": token}), nil))
	srv := httptest.NewServer(NewServer(Config{Token: token, Timeout: timeout}, reg, log))
	t.Cleanup(srv.Close)
	return srv, &logs
}

func post(t *testing.T, srv *httptest.Server, body string, headers ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/scrape", strings.NewReader(body))
	require.NoError(t, err)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req) // httptest server on loopback
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func auth() []string { return []string{"Authorization", "Bearer " + token} }

func errBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var v struct {
		Error map[string]any `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &v), string(b))
	require.NotNil(t, v.Error, string(b))
	return v.Error
}

func TestHealthWithoutAuth(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	assert.Equal(t, 200, resp.StatusCode)
	assert.JSONEq(t, `{"status":"ok"}`, string(b))
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
}

func TestUnauthorizedBeforeBodyIsRead(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	for name, h := range map[string][]string{
		"missing":   nil,
		"wrong":     {"Authorization", "Bearer wrong"},
		"no scheme": {"Authorization", token},
		"basic":     {"Authorization", "Basic " + token},
		"empty":     {"Authorization", "Bearer "},
	} {
		t.Run(name, func(t *testing.T) {
			// A body that would be 400 invalid_request proves auth is checked before the body is parsed.
			resp, b := post(t, srv, "{not json", h...)
			assert.Equal(t, 401, resp.StatusCode)
			assert.Equal(t, "Bearer", resp.Header.Get("WWW-Authenticate"))
			assert.Equal(t, "unauthorized", errBody(t, b)["code"])
			assert.NotContains(t, string(b), token)
		})
	}
}

func TestAuthDoesNotReadBody(t *testing.T) {
	h := NewServer(Config{Token: token, Timeout: time.Second}, NewRegistry(), nil)
	r := httptest.NewRequest(http.MethodPost, "/scrape", &failingReader{t})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	assert.Equal(t, 401, rec.Code)
}

type failingReader struct{ t *testing.T }

func (f *failingReader) Read([]byte) (int, error) {
	f.t.Error("the body was read before authentication")
	return 0, io.EOF
}

func TestInvalidRequest(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	for _, body := range []string{"{not json", "", `["x"]`, "null", `"s"`, "5"} {
		resp, b := post(t, srv, body, auth()...)
		assert.Equal(t, 400, resp.StatusCode, body)
		assert.Equal(t, "invalid_request", errBody(t, b)["code"], body)
	}
}

func TestValidationFailed(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	long := `{"url": "https://example.com/` + strings.Repeat("a", 2048) + `"}`
	for name, c := range map[string]struct {
		body   string
		fields []string
	}{
		"missing url":        {`{}`, []string{"url"}},
		"url number":         {`{"url": 5}`, []string{"url"}},
		"url null":           {`{"url": null}`, []string{"url"}},
		"relative":           {`{"url": "/relative/path"}`, []string{"url"}},
		"no scheme":          {`{"url": "not a url"}`, []string{"url"}},
		"ftp":                {`{"url": "ftp://bsky.app/profile/a"}`, []string{"url"}},
		"no host":            {`{"url": "https:///x"}`, []string{"url"}},
		"too long":           {long, []string{"url"}},
		"posts zero":         {`{"url": "https://bsky.app/profile/a", "max_posts": 0}`, []string{"max_posts"}},
		"posts 101":          {`{"url": "https://bsky.app/profile/a", "max_posts": 101}`, []string{"max_posts"}},
		"posts string":       {`{"url": "https://bsky.app/profile/a", "max_posts": "5"}`, []string{"max_posts"}},
		"posts bool":         {`{"url": "https://bsky.app/profile/a", "max_posts": true}`, []string{"max_posts"}},
		"posts null":         {`{"url": "https://bsky.app/profile/a", "max_posts": null}`, []string{"max_posts"}},
		"posts 1.5":          {`{"url": "https://bsky.app/profile/a", "max_posts": 1.5}`, []string{"max_posts"}},
		"comments -1":        {`{"url": "https://bsky.app/profile/a", "max_comments": -1}`, []string{"max_comments"}},
		"comments 201":       {`{"url": "https://bsky.app/profile/a", "max_comments": 201}`, []string{"max_comments"}},
		"comments array":     {`{"url": "https://bsky.app/profile/a", "max_comments": [1]}`, []string{"max_comments"}},
		"every member named": {`{"url": 1, "max_posts": "x", "max_comments": 1.5}`, []string{"url", "max_posts", "max_comments"}},
	} {
		t.Run(name, func(t *testing.T) {
			resp, b := post(t, srv, c.body, auth()...)
			assert.Equal(t, 422, resp.StatusCode)
			e := errBody(t, b)
			assert.Equal(t, "validation_failed", e["code"])
			fields := e["details"].(map[string]any)["fields"].(map[string]any)
			assert.Len(t, fields, len(c.fields))
			for _, f := range c.fields {
				assert.Contains(t, fields, f)
			}
		})
	}
}

func TestDefaultsAndUnknownMembers(t *testing.T) {
	reg, mod := NewRegistry(), &okModule{}
	reg.Register(Bluesky, mod)
	srv, _ := newTestServer(t, time.Second, reg)

	resp, b := post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social?x=1", "lang": "en"}`, auth()...)
	require.Equal(t, 200, resp.StatusCode, string(b))
	assert.Equal(t, Request{MaxPosts: 20, MaxComments: 50}, mod.got)
	assert.Equal(t, map[string]string{"actor": "alice.bsky.social"}, mod.url.IDs)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, "bluesky", got["platform"])
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$`, got["scraped_at"])
	// nil slices become [] and absent members null, as the shared spec requires
	assert.Equal(t, []any{}, got["posts"])
	assert.Equal(t, []any{}, got["comments"])
	assert.Equal(t, []any{}, got["warnings"])
	assert.Contains(t, got, "post")
	assert.Nil(t, got["post"])
	assert.Nil(t, got["followers"])
	assert.IsType(t, float64(0), got["latency_ms"])

	resp, _ = post(t, srv, `{"url": "https://bsky.app/profile/a", "max_posts": 100.0, "max_comments": 0}`, auth()...)
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, Request{MaxPosts: 100, MaxComments: 0}, mod.got)
}

func TestUnsupportedAndPersonalProfile(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	resp, b := post(t, srv, `{"url": "https://example.com/some/article"}`, auth()...)
	assert.Equal(t, 422, resp.StatusCode)
	assert.Equal(t, "unsupported_url", errBody(t, b)["code"])

	resp, b = post(t, srv, `{"url": "https://www.facebook.com/profile.php?id=100000000000001"}`, auth()...)
	assert.Equal(t, 422, resp.StatusCode)
	e := errBody(t, b)
	assert.Equal(t, "unsupported_account_type", e["code"])
	assert.Equal(t, "personal_profile", e["details"].(map[string]any)["account_type"])
}

func TestPlatformNotConfigured(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	for url, platform := range map[string]string{
		"https://www.instagram.com/alice":            "instagram",
		"https://threads.net/@alice":                 "threads",
		"https://www.facebook.com/examplenews":       "facebook",
		"https://x.com/alice":                        "twitter",
		"https://bsky.app/profile/alice.bsky.social": "bluesky",
		"https://mastodon.social/@alice":             "mastodon",
	} {
		resp, b := post(t, srv, `{"url": "`+url+`"}`, auth()...)
		assert.Equal(t, 503, resp.StatusCode, url)
		e := errBody(t, b)
		assert.Equal(t, "platform_not_configured", e["code"], url)
		assert.Equal(t, platform, e["details"].(map[string]any)["platform"], url)
	}
}

func TestModuleTypedErrorGetsPlatformAndHeaders(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Mastodon, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		e := RateLimited("", 42)
		return nil, e
	}))
	srv, _ := newTestServer(t, time.Second, reg)
	resp, b := post(t, srv, `{"url": "https://mastodon.social/@alice"}`, auth()...)
	assert.Equal(t, 429, resp.StatusCode)
	assert.Equal(t, "42", resp.Header.Get("Retry-After"))
	e := errBody(t, b)
	assert.Equal(t, "mastodon", e["details"].(map[string]any)["platform"])
	assert.EqualValues(t, 42, e["details"].(map[string]any)["retry_after_s"])
}

func TestPanicIs500WithoutStackTrace(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Bluesky, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		panic("secret-panic-detail " + token)
	}))
	srv, logs := newTestServer(t, time.Second, reg)
	resp, b := post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social"}`, auth()...)
	assert.Equal(t, 500, resp.StatusCode)
	assert.Equal(t, "internal_error", errBody(t, b)["code"])
	for _, bad := range []string{"goroutine", "panic", ".go:", "secret-panic-detail", token} {
		assert.NotContains(t, string(b), bad)
	}
	assert.Contains(t, logs.String(), "stack") // the trace is in the log, redacted
	assert.NotContains(t, logs.String(), token)
	// the server survives
	resp, _ = post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social"}`, auth()...)
	assert.Equal(t, 500, resp.StatusCode)
}

func TestPlainErrorIs500(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Bluesky, moduleFunc(func(context.Context, Target, Request) (*Result, error) {
		return nil, errors.New("boom " + token)
	}))
	srv, logs := newTestServer(t, time.Second, reg)
	resp, b := post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social"}`, auth()...)
	assert.Equal(t, 500, resp.StatusCode)
	assert.NotContains(t, string(b), "boom")
	assert.NotContains(t, logs.String(), token)
}

func TestTimeoutIs504(t *testing.T) {
	for name, fn := range map[string]moduleFunc{
		"honours ctx": func(ctx context.Context, _ Target, _ Request) (*Result, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		"ignores ctx": func(context.Context, Target, Request) (*Result, error) {
			time.Sleep(300 * time.Millisecond)
			return &Result{}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(Bluesky, fn)
			srv, _ := newTestServer(t, 50*time.Millisecond, reg)
			start := time.Now()
			resp, b := post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social"}`, auth()...)
			assert.Equal(t, 504, resp.StatusCode)
			e := errBody(t, b)
			assert.Equal(t, "upstream_timeout", e["code"])
			assert.Equal(t, "bluesky", e["details"].(map[string]any)["platform"])
			assert.Less(t, time.Since(start), 250*time.Millisecond)
		})
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	srv, _ := newTestServer(t, time.Second, NewRegistry())
	resp, err := http.Get(srv.URL + "/nope")
	require.NoError(t, err)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, 404, resp.StatusCode)
	assert.Equal(t, "invalid_request", errBody(t, b)["code"])

	resp, err = http.Get(srv.URL + "/scrape")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, 405, resp.StatusCode)
	assert.Equal(t, "POST", resp.Header.Get("Allow"))
}

func TestSentinelTokenNeverLeaks(t *testing.T) {
	const sentinel = "SENTINEL-tok-9f8e7d6c5b4a"
	t.Setenv("SCRAPER_TOKEN", sentinel)
	var logs bytes.Buffer
	log := NewLogger(&logs, NewRedactor(func(k string) string { return map[string]string{"SCRAPER_TOKEN": sentinel}[k] }, nil))
	reg := NewRegistry()
	reg.Register(Bluesky, moduleFunc(func(_ context.Context, t Target, _ Request) (*Result, error) {
		switch t.IDs["actor"] {
		case "panics":
			panic("token is " + sentinel)
		case "errors":
			return nil, errors.New("Authorization: Bearer " + sentinel)
		}
		return &Result{Platform: Bluesky, Username: t.IDs["actor"]}, nil
	}))
	srv := httptest.NewServer(NewServer(Config{Token: sentinel, Timeout: time.Second}, reg, log))
	defer srv.Close()

	var seen strings.Builder
	do := func(method, path, body string, hdr ...string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		seen.Write(b)
		for k, v := range resp.Header {
			seen.WriteString(k + ": " + strings.Join(v, ",") + "\n")
		}
	}
	bearer := []string{"Authorization", "Bearer " + sentinel}
	do("POST", "/scrape", `{"url":"https://bsky.app/profile/ok"}`, bearer...)
	do("POST", "/scrape", `{"url":"https://bsky.app/profile/panics"}`, bearer...)
	do("POST", "/scrape", `{"url":"https://bsky.app/profile/errors"}`, bearer...)
	do("POST", "/scrape", `{"url":"https://example.com/`+sentinel+`"}`, bearer...)
	do("POST", "/scrape", `{"url":"https://bsky.app/profile/ok"}`, "Authorization", "Bearer "+sentinel+"x")
	do("POST", "/scrape", `{"url":"https://bsky.app/profile/ok"}`, "Authorization", sentinel)
	do("GET", "/"+sentinel, "", bearer...)

	assert.NotContains(t, seen.String(), sentinel, "response bodies and headers")
	assert.NotContains(t, logs.String(), sentinel, "logs")
	assert.Contains(t, logs.String(), `"method":"POST"`) // access lines exist
}

func TestAccessLogLine(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Bluesky, &okModule{})
	srv, logs := newTestServer(t, time.Second, reg)
	post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social/post/3k"}`, append(auth(), "X-Custom", "header-value")...)
	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		require.NoError(t, json.Unmarshal([]byte(l), &line))
	}
	assert.Equal(t, "POST", line["method"])
	assert.Equal(t, "/scrape", line["path"])
	assert.EqualValues(t, 200, line["status"])
	assert.Equal(t, "bluesky", line["platform"])
	assert.Equal(t, "post", line["url_kind"])
	assert.Contains(t, line, "latency_ms")
	assert.NotContains(t, logs.String(), "header-value")
}

func TestModuleErrorLiteralWithNilDetailsAndSharedError(t *testing.T) {
	shared := &Error{Code: "upstream_error", Message: "x", Status: 502} // nil Details, shared by every request
	reg := NewRegistry()
	reg.Register(Bluesky, moduleFunc(func(context.Context, Target, Request) (*Result, error) { return nil, shared }))
	srv, _ := newTestServer(t, time.Second, reg)
	for range 3 {
		resp, b := post(t, srv, `{"url": "https://bsky.app/profile/alice.bsky.social"}`, auth()...)
		assert.Equal(t, 502, resp.StatusCode)
		e := errBody(t, b)
		assert.Equal(t, "upstream_error", e["code"])
		assert.Equal(t, "bluesky", e["details"].(map[string]any)["platform"])
	}
	assert.Nil(t, shared.Details) // the module's value is not mutated
}
