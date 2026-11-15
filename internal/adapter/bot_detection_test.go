package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

func botReq(text string) Request {
	return Request{JobID: "j1", AnalyzerID: "bot_detection", Inputs: map[string]any{"content": map[string]any{
		"content": "<p>" + text + "</p>", "normalized_content": text, "content_type": "text/html", "source": "s"}}}
}

// botStub answers every request with status and body and records the last request.
func botStub(t *testing.T, status int, body string) (*registry.Analyzer, *struct {
	calls atomic.Int32
	body  string
	hdr   http.Header
	meth  string
}) {
	rec := &struct {
		calls atomic.Int32
		body  string
		hdr   http.Header
		meth  string
	}{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.body, rec.hdr, rec.meth = string(b), r.Header, r.Method
		rec.calls.Add(1)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	d := def(registry.AdapterBotDetection, srv.URL)
	d.ID = "bot_detection"
	return d, rec
}

func TestBotDetectionRequest(t *testing.T) {
	d, rec := botStub(t, 200, `{"label":"LABEL_1","score":0.87}`)
	got, err := (&Executor{}).Execute(context.Background(), d, botReq("Это пример текста."))
	require.NoError(t, err)
	require.JSONEq(t, `{"label":"bot_or_generated","score":0.87,"scope":"text","model_label":"LABEL_1","model_score":0.87}`, string(got))
	require.EqualValues(t, 1, rec.calls.Load())
	require.Equal(t, "POST", rec.meth)
	require.JSONEq(t, `{"text":"Это пример текста."}`, rec.body)
	require.Equal(t, "application/json", rec.hdr.Get("Content-Type"))
	require.Equal(t, "Bearer tok", rec.hdr.Get("Authorization"))
}

func TestBotDetectionResponseFormsAndMapping(t *testing.T) {
	cases := []struct {
		name, answer, label string
		score               float64
		mlabel              string
		mscore              float64
	}{
		{"object bot", `{"label":"LABEL_1","score":0.87}`, "bot_or_generated", 0.87, "LABEL_1", 0.87},
		{"object human", `{"label":"LABEL_0","score":0.9}`, "human", 0.1, "LABEL_0", 0.9},
		{"human p=1", `{"label":"LABEL_0","score":1}`, "human", 0, "LABEL_0", 1},
		{"human p=0", `{"label":"LABEL_0","score":0}`, "human", 1, "LABEL_0", 0},
		{"bot p=0", `{"label":"LABEL_1","score":0}`, "bot_or_generated", 0, "LABEL_1", 0},
		{"bot p=1", `{"label":"LABEL_1","score":1}`, "bot_or_generated", 1, "LABEL_1", 1},
		{"list", `[{"label":"LABEL_0","score":0.7}]`, "human", 0.3, "LABEL_0", 0.7},
		{"nested list", `[[{"label":"LABEL_1","score":0.6}]]`, "bot_or_generated", 0.6, "LABEL_1", 0.6},
		{"result wrapper", `{"result":[{"label":"LABEL_1","score":0.55}],"latency_ms":3}`, "bot_or_generated", 0.55, "LABEL_1", 0.55},
		{"result nested", `{"result":[[{"label":"LABEL_0","score":0.8}]]}`, "human", 0.2, "LABEL_0", 0.8},
		{"both, human top", `[[{"label":"LABEL_1","score":0.25},{"label":"LABEL_0","score":0.75}]]`, "human", 0.25, "LABEL_0", 0.75},
		{"both, bot top", `[{"label":"LABEL_0","score":0.1},{"label":"LABEL_1","score":0.9}]`, "bot_or_generated", 0.9, "LABEL_1", 0.9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, _ := botStub(t, 200, c.answer)
			got, err := (&Executor{}).Execute(context.Background(), d, botReq("some text"))
			require.NoError(t, err)
			var r map[string]any
			require.NoError(t, json.Unmarshal(got, &r))
			require.Equal(t, map[string]any{"label": c.label, "score": c.score, "scope": "text",
				"model_label": c.mlabel, "model_score": c.mscore}, r)
		})
	}
}

func TestBotDetectionInvalidOutput(t *testing.T) {
	for name, answer := range map[string]string{
		"unknown label":     `{"label":"LABEL_7","score":0.8}`,
		"unknown in list":   `[{"label":"LABEL_1","score":0.1},{"label":"bot","score":0.9}]`,
		"label missing":     `{"score":0.8}`,
		"score missing":     `{"label":"LABEL_1"}`,
		"score string":      `{"label":"LABEL_1","score":"0.8"}`,
		"score null":        `{"label":"LABEL_1","score":null}`,
		"score above 1":     `{"label":"LABEL_1","score":1.2}`,
		"score negative":    `{"label":"LABEL_0","score":-0.1}`,
		"duplicate label":   `[{"label":"LABEL_1","score":0.5},{"label":"LABEL_1","score":0.4}]`,
		"empty object":      `{}`,
		"empty list":        `[]`,
		"empty nested list": `[[]]`,
		"empty result":      `{"result":[]}`,
		"string":            `"LABEL_1"`,
		"number":            `0.8`,
		"list of numbers":   `[0.8]`,
		"not json":          `<html>`,
		"empty body":        ``,
		"null":              `null`,
	} {
		t.Run(name, func(t *testing.T) {
			d, _ := botStub(t, 200, answer)
			_, err := (&Executor{}).Execute(context.Background(), d, botReq("some text"))
			require.Equal(t, job.CodeInvalidOutput, runErr(t, err).Code)
		})
	}
}

func TestBotDetectionAdapterErrors(t *testing.T) {
	// empty or missing text: no request
	d, rec := botStub(t, 200, `{"label":"LABEL_1","score":0.5}`)
	for _, in := range []map[string]any{
		{"content": map[string]any{"normalized_content": ""}},
		{"content": map[string]any{"normalized_content": "  \n"}},
		{"content": map[string]any{"content": "x"}},
		{},
	} {
		_, err := (&Executor{}).Execute(context.Background(), d, Request{JobID: "j", AnalyzerID: "bot_detection", Inputs: in})
		require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	}
	// a translation binding is never read
	_, err := (&Executor{}).Execute(context.Background(), d, Request{JobID: "j", AnalyzerID: "bot_detection",
		Inputs: map[string]any{"text": map[string]any{"translated_text": "hello"}}})
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
	require.Zero(t, rec.calls.Load())

	// non-2xx: status only, echoed credentials hidden
	d, _ = botStub(t, 401, `{"error":"bad token Bearer tok","header":"tok"}`)
	_, err = (&Executor{}).Execute(context.Background(), d, botReq("x"))
	re := runErr(t, err)
	require.Equal(t, job.CodeAdapterError, re.Code)
	require.Equal(t, "analyzer service returned HTTP 401", re.Message)

	// connection error
	d, _ = botStub(t, 200, "")
	d.Execution.Endpoint = "http://127.0.0.1:1/predict"
	_, err = (&Executor{}).Execute(context.Background(), d, botReq("x"))
	require.Equal(t, job.CodeAdapterError, runErr(t, err).Code)
}

func TestBotDetectionTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) // lets the server notice the client's disconnect
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()
	d := def(registry.AdapterBotDetection, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (&Executor{}).Execute(ctx, d, botReq("x"))
	require.Equal(t, job.CodeTimeout, runErr(t, err).Code)
}

func TestBotDetectionErrorsHideCredentials(t *testing.T) {
	// the service echoes the configured header value and token in its answer
	d, _ := botStub(t, 200, `{"label":"Bearer tok","score":0.5,"echo":"Authorization: Bearer tok"}`)
	_, err := (&Executor{}).Execute(context.Background(), d, botReq("x"))
	re := runErr(t, err)
	require.Equal(t, job.CodeInvalidOutput, re.Code)
	require.NotContains(t, re.Message, "tok")
	require.NotContains(t, re.Message, "Bearer")
}
