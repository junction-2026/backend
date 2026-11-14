package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/registry"
)

const (
	maxBody      = 32 << 20
	abortTimeout = time.Second // best-effort DELETE; keeps total return under 2s of cancellation
)

func (x *Executor) client() *http.Client {
	if x.Client != nil {
		return x.Client
	}
	return noRedirects
}

// noRedirects: adapters never retry or follow redirects, so configured
// credential headers cannot reach another host through a 3xx answer (which
// then fails as a non-2xx response).
var noRedirects = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// send performs one request. Configured headers (credentials) go to target
// only when it shares scheme and host with the configured endpoint.
// Error messages never contain URLs.
func (x *Executor) send(ctx context.Context, def *registry.Analyzer, req Request, method, target string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	hr, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, adapterErr("invalid analyzer request")
	}
	if sameOrigin(def.Execution.Endpoint, target) {
		for k, v := range def.Execution.Headers {
			hr.Header.Set(k, v)
		}
	}
	if body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	hr.Header.Set("Accept", "application/json")
	hr.Header.Set("X-Analysis-Job-Id", req.JobID)
	hr.Header.Set("X-Analysis-Analyzer-Id", req.AnalyzerID)
	resp, err := x.client().Do(hr)
	if err != nil {
		if e := ContextError(ctx); e != nil {
			return nil, e
		}
		return nil, adapterErr("analyzer service request failed")
	}
	return resp, nil
}

func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

// readObject drains resp and returns its body if it is a JSON object.
func readObject(ctx context.Context, resp *http.Response) (json.RawMessage, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if e := ContextError(ctx); e != nil {
			return nil, e
		}
		return nil, adapterErr("reading analyzer response failed")
	}
	b = bytes.TrimSpace(b)
	if !json.Valid(b) || len(b) == 0 || b[0] != '{' {
		return nil, adapterErr("analyzer response is not a JSON object")
	}
	return b, nil
}

func (x *Executor) submit(ctx context.Context, def *registry.Analyzer, req Request) (*http.Response, error) {
	body, err := json.Marshal(map[string]any{"job_id": req.JobID, "analyzer_id": req.AnalyzerID, "inputs": req.Inputs})
	if err != nil {
		return nil, adapterErr("analyzer inputs are not serializable")
	}
	return x.send(ctx, def, req, def.Execution.Method, def.Execution.Endpoint, body)
}

func (x *Executor) httpSync(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	resp, err := x.submit(ctx, def, req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, adapterErr("analyzer service returned HTTP %d", resp.StatusCode)
	}
	return readObject(ctx, resp)
}

func (x *Executor) httpAsync(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	resp, err := x.submit(ctx, def, req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return readObject(ctx, resp)
	case http.StatusAccepted:
	default:
		resp.Body.Close()
		return nil, adapterErr("analyzer service returned HTTP %d", resp.StatusCode)
	}
	b, err := readObject(ctx, resp)
	if err != nil {
		return nil, err
	}
	var acc struct {
		StatusURL string `json:"status_url"`
	}
	json.Unmarshal(b, &acc)
	if u, err := url.Parse(acc.StatusURL); err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, adapterErr("analyzer service did not return a valid status_url")
	}
	return x.poll(ctx, def, req, acc.StatusURL)
}

func (x *Executor) poll(ctx context.Context, def *registry.Analyzer, req Request, statusURL string) (json.RawMessage, error) {
	t := time.NewTimer(def.Execution.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, x.abort(ctx, def, req, statusURL)
		case <-t.C:
		}
		resp, err := x.send(ctx, def, req, http.MethodGet, statusURL, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil, x.abort(ctx, def, req, statusURL)
			}
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, adapterErr("status poll returned HTTP %d", resp.StatusCode)
		}
		b, err := readObject(ctx, resp)
		if err != nil {
			if ctx.Err() != nil {
				return nil, x.abort(ctx, def, req, statusURL)
			}
			return nil, err
		}
		var st struct {
			Status string          `json:"status"`
			Result json.RawMessage `json:"result"`
			Error  struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		json.Unmarshal(b, &st)
		switch st.Status {
		case "succeeded":
			if len(st.Result) == 0 || st.Result[0] != '{' {
				return nil, adapterErr("succeeded status without a result object")
			}
			return st.Result, nil
		case "failed":
			msg := "analyzer service reported failure"
			if m := strings.TrimSpace(st.Error.Message); m != "" {
				msg += ": " + truncate(m, 200)
			}
			return nil, adapterErr("%s", msg)
		}
		t.Reset(def.Execution.PollInterval) // pending, running, unknown
	}
}

// abort sends the one best-effort DELETE (answer ignored) and returns the
// timeout/cancelled error of ctx.
func (x *Executor) abort(ctx context.Context, def *registry.Analyzer, req Request, statusURL string) error {
	actx, cancel := context.WithTimeout(context.Background(), abortTimeout)
	defer cancel()
	if resp, err := x.send(actx, def, req, http.MethodDelete, statusURL, nil); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
	}
	return ContextError(ctx)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
