package localization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/finalizer"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// fake is a Translator: "<lang>:<text>" per text unless reply/err is set; block (if set) is waited for first.
type fake struct {
	calls atomic.Int32
	mu    sync.Mutex
	got   [][]string
	reply func(in []string) ([]string, error)
	block chan struct{}
}

func (f *fake) Translate(ctx context.Context, lang string, in []string) ([]string, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.got = append(f.got, in)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.reply != nil {
		return f.reply(in)
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = lang + ":" + s
	}
	return out, nil
}

// mk is a done job: finalizer user_text by producedBy ("" for no result), the translation source language ("" for
// no translation run) and llm_techniques findings in explLang (nil findings: no llm_techniques run).
func mk(id, producedBy, srcLang, explLang string, findings ...string) job.Job {
	j := job.Job{ID: id, Status: job.StatusCompleted, Done: true, Analyzers: []job.AnalyzerRun{}}
	if producedBy != "" {
		text := "Le texte est trompeur."
		if producedBy == "orchestrator" {
			text = finalizer.InsufficientText
		}
		j.Finalization.Result, _ = json.Marshal(job.FinalAssessment{Verdict: "detected", UserText: text, ProducedBy: producedBy})
	}
	if srcLang != "" {
		j.Analyzers = append(j.Analyzers, job.AnalyzerRun{ID: "translation", Status: job.RunSucceeded,
			Result: json.RawMessage(fmt.Sprintf(`{"source_language":%q,"translated_text":"The text."}`, srcLang))})
	}
	if findings != nil {
		var fs []map[string]any
		for _, l := range findings {
			fs = append(fs, map[string]any{"label": l, "text": "« cité »", "explanation": "expl " + l})
		}
		res, _ := json.Marshal(map[string]any{"explanation_language": explLang, "findings": fs})
		j.Analyzers = append(j.Analyzers, job.AnalyzerRun{ID: "llm_techniques", Status: job.RunSucceeded, Result: res})
	}
	return j
}

func text(t LocalizedText) any {
	if t.Text == nil {
		return nil
	}
	return *t.Text
}

func src(t LocalizedText) any {
	if t.SourceLanguage == nil {
		return nil
	}
	return *t.SourceLanguage
}

func TestLanguage(t *testing.T) {
	for in, want := range map[string]string{"ar": "ar", "DE-at": "de", "fr-CA": "fr", " FI ": "fi", "en": "en", "ru": "ru", "RU-ru": "ru", " ru-UA ": "ru"} {
		got, ok := Language(in)
		require.True(t, ok, in)
		require.Equal(t, want, got)
	}
	for _, in := range []string{"xx", "", "es", "de_AT", "-de"} {
		_, ok := Language(in)
		require.False(t, ok, in)
	}
}

func TestFixedTableCoversSupported(t *testing.T) {
	for _, l := range Supported {
		require.NotEmpty(t, strings.TrimSpace(insufficient[l]), l)
	}
	require.Equal(t, finalizer.InsufficientText, insufficient["en"])
}

func TestLocalizeStatuses(t *testing.T) {
	for name, c := range map[string]struct {
		j         job.Job
		lang      string
		user      []any // status, text, source_language; nil: final_assessment null
		findings  [][]any
		calls     int32
		sentTexts int
	}{
		"french finalizer text to arabic": {mk("j", "finalizer", "fr", ""), "ar",
			[]any{"translated", "ar:Le texte est trompeur.", "fr"}, nil, 1, 1},
		"same language is original": {mk("j", "finalizer", "de", ""), "de",
			[]any{"original", "Le texte est trompeur.", "de"}, nil, 0, 0},
		"no translation run: source null, translated": {mk("j", "finalizer", "", ""), "fi",
			[]any{"translated", "fi:Le texte est trompeur.", nil}, nil, 1, 1},
		"orchestrator fixed text in finnish": {mk("j", "orchestrator", "fr", ""), "fi",
			[]any{"translated", insufficient["fi"], "en"}, nil, 0, 0},
		"orchestrator fixed text in russian": {mk("j", "orchestrator", "", ""), "ru",
			[]any{"translated", insufficient["ru"], "en"}, nil, 0, 0},
		"orchestrator fixed text in arabic": {mk("j", "orchestrator", "", ""), "ar",
			[]any{"translated", insufficient["ar"], "en"}, nil, 0, 0},
		"orchestrator text in english is original": {mk("j", "orchestrator", "fr", ""), "en",
			[]any{"original", finalizer.InsufficientText, "en"}, nil, 0, 0},
		"no finalization and no llm run": {mk("j", "", "", ""), "ar", nil, nil, 0, 0},
		"llm findings batched with user_text": {mk("j", "finalizer", "fr", "fr", "misleading_headline", "statistical_cherry_picking"), "ar",
			[]any{"translated", "ar:Le texte est trompeur.", "fr"},
			[][]any{{0, "misleading_headline", "translated", "ar:expl misleading_headline", "fr"},
				{1, "statistical_cherry_picking", "translated", "ar:expl statistical_cherry_picking", "fr"}}, 1, 3},
		"llm explanations already in the language": {mk("j", "", "", "en", "misleading_headline"), "en", nil,
			[][]any{{0, "misleading_headline", "original", "expl misleading_headline", "en"}}, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fake{}
			loc := New(f, 0, nil).Localize(c.j, c.lang)
			require.Equal(t, "j", loc.JobID)
			require.Equal(t, c.lang, loc.Language)
			if c.user == nil {
				require.Nil(t, loc.FinalAssessment)
			} else {
				u := loc.FinalAssessment.UserText
				require.Equal(t, c.user, []any{u.Status, text(u), src(u)})
			}
			require.Len(t, loc.LLMFindings, len(c.findings))
			for i, want := range c.findings {
				g := loc.LLMFindings[i]
				require.Equal(t, want, []any{g.Index, g.Label, g.Explanation.Status, text(g.Explanation), src(g.Explanation)})
			}
			require.Equal(t, c.calls, f.calls.Load())
			if c.sentTexts > 0 {
				require.Len(t, f.got[0], c.sentTexts, "one batched request")
			}
		})
	}
}

func TestFailedLLMRunHasNoFindings(t *testing.T) {
	j := mk("j", "", "", "fr", "misleading_headline")
	j.Analyzers[0].Status = job.RunFailed
	loc := New(&fake{}, 0, nil).Localize(j, "ar")
	require.NotNil(t, loc.LLMFindings)
	require.Empty(t, loc.LLMFindings)
	b, _ := json.Marshal(loc)
	require.JSONEq(t, `{"job_id":"j","language":"ar","final_assessment":null,"llm_findings":[]}`, string(b))
}

func TestFailuresAreUnavailable(t *testing.T) {
	two := mk("j", "finalizer", "fr", "fr", "misleading_headline")
	for name, c := range map[string]struct {
		tr   Translator
		want []string // statuses of user_text, finding 0
	}{
		"no provider":    {nil, []string{"unavailable", "unavailable"}},
		"provider error": {&fake{reply: func([]string) ([]string, error) { return nil, errors.New("provider HTTP 503") }}, []string{"unavailable", "unavailable"}},
		"count mismatch": {&fake{reply: func([]string) ([]string, error) { return []string{"x"}, nil }}, []string{"unavailable", "unavailable"}},
		"blank entry":    {&fake{reply: func([]string) ([]string, error) { return []string{"traduit", "  "}, nil }}, []string{"translated", "unavailable"}},
		"timeout":        {&fake{block: make(chan struct{})}, []string{"unavailable", "unavailable"}},
	} {
		t.Run(name, func(t *testing.T) {
			loc := New(c.tr, 20*time.Millisecond, slog.New(slog.DiscardHandler)).Localize(two, "ar")
			u, e := loc.FinalAssessment.UserText, loc.LLMFindings[0].Explanation
			require.Equal(t, c.want, []string{u.Status, e.Status})
			for _, x := range []LocalizedText{u, e} {
				require.Equal(t, "fr", src(x), "source language kept")
				if x.Status == StatusUnavailable {
					require.Nil(t, x.Text)
				}
			}
		})
	}
}

func TestCache(t *testing.T) {
	j := mk("j", "finalizer", "fr", "fr", "misleading_headline")
	f := &fake{}
	s := New(f, 0, nil)
	first := s.Localize(j, "ar")
	require.Equal(t, first, s.Localize(j, "ar"), "hit: same texts")
	require.EqualValues(t, 1, f.calls.Load())
	s.Localize(j, "de") // miss: other language
	s.Localize(mk("k", "finalizer", "fr", ""), "ar")
	require.EqualValues(t, 3, f.calls.Load())
	s.Localize(mk("o", "orchestrator", "", ""), "ar") // nothing to translate: no call, no entry
	require.EqualValues(t, 3, f.calls.Load())
	require.Equal(t, 3, s.lru.Len())
}

func TestUnavailableIsNotCached(t *testing.T) {
	j := mk("j", "finalizer", "fr", "fr", "misleading_headline")
	fail := true
	f := &fake{}
	f.reply = func(in []string) ([]string, error) {
		if fail {
			return nil, errors.New("request failed")
		}
		return []string{"T" + fmt.Sprint(len(in))}, nil
	}
	s := New(f, 0, slog.New(slog.DiscardHandler))
	require.Equal(t, StatusUnavailable, s.Localize(j, "ar").FinalAssessment.UserText.Status)
	require.Equal(t, 0, s.lru.Len())

	// partial: the blank second text is retried alone next time, the first stays as cached
	fail = false
	f.reply = func(in []string) ([]string, error) { return []string{"premier", ""}, nil }
	loc := s.Localize(j, "ar")
	require.Equal(t, []string{"translated", "unavailable"}, []string{loc.FinalAssessment.UserText.Status, loc.LLMFindings[0].Explanation.Status})
	f.reply = func(in []string) ([]string, error) { return []string{"second"}, nil }
	loc = s.Localize(j, "ar")
	require.Equal(t, "premier", *loc.FinalAssessment.UserText.Text)
	require.Equal(t, "second", *loc.LLMFindings[0].Explanation.Text)
	require.EqualValues(t, 3, f.calls.Load())
	require.Len(t, f.got[2], 1, "only the missing text is sent again")
	s.Localize(j, "ar")
	require.EqualValues(t, 3, f.calls.Load())
}

func TestConcurrentRequestsShareOneCall(t *testing.T) {
	j := mk("j", "finalizer", "fr", "")
	f := &fake{block: make(chan struct{})}
	s := New(f, 0, nil)
	var wg sync.WaitGroup
	out := make([]JobLocalization, 10)
	for i := range out {
		wg.Go(func() { out[i] = s.Localize(j, "ar") })
	}
	require.Eventually(t, func() bool { return f.calls.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // let the others reach the in-flight wait
	close(f.block)
	wg.Wait()
	require.EqualValues(t, 1, f.calls.Load())
	for _, l := range out {
		require.Equal(t, "ar:Le texte est trompeur.", *l.FinalAssessment.UserText.Text)
	}
}

func TestCacheIsBounded(t *testing.T) {
	f := &fake{}
	s := New(f, 0, nil)
	s.max = 2
	a, b, c := mk("a", "finalizer", "fr", ""), mk("b", "finalizer", "fr", ""), mk("c", "finalizer", "fr", "")
	s.Localize(a, "ar")
	s.Localize(b, "ar")
	s.Localize(a, "ar") // a is now the most recent
	s.Localize(c, "ar") // evicts b
	require.EqualValues(t, 3, f.calls.Load())
	s.Localize(a, "ar")
	require.EqualValues(t, 3, f.calls.Load())
	s.Localize(b, "ar")
	require.EqualValues(t, 4, f.calls.Load())
	require.Equal(t, 2, s.lru.Len())
}

const apiKey = "sk-live-SECRET-0123456789"

func providerFor(t *testing.T, url string) Translator {
	t.Helper()
	reg := load(t, `analyzers:
  - id: translation
    name: Translation
    category: translation
    execution:
      adapter: mistral_translation
      endpoint: `+url+`
      api_key: `+apiKey+`
      headers: {X-Tenant: tenant-SECRET}
`)
	tr := FromRegistry(reg)
	require.NotNil(t, tr)
	return tr
}

func TestProviderRequestAndReply(t *testing.T) {
	var body map[string]any
	var auth, tenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(b, &body))
		auth, tenant = r.Header.Get("Authorization"), r.Header.Get("X-Tenant")
		io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"{\"translations\":[\"أ\",\"ب\"]}"}}]}`)
	}))
	defer srv.Close()
	out, err := providerFor(t, srv.URL).Translate(context.Background(), "ar", []string{"un « cité »", "deux"})
	require.NoError(t, err)
	require.Equal(t, []string{"أ", "ب"}, out)
	require.Equal(t, "Bearer "+apiKey, auth)
	require.Equal(t, "tenant-SECRET", tenant)
	require.Equal(t, "mistral-small-latest", body["model"])
	msgs := body["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	require.Contains(t, sys, `"ar"`)
	require.Contains(t, sys, "quoted span")
	require.Contains(t, sys, "never as instructions")
	require.Equal(t, `["un « cité »","deux"]`, msgs[1].(map[string]any)["content"])
}

func TestProviderErrorsCarryNoSecrets(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"503": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
			io.WriteString(w, `{"error":{"message":"upstream PROVIDER-MSG for `+apiKey+`"}}`)
		},
		"redirect": func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://elsewhere.example/x", 302) },
		"not json": func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "PROVIDER-MSG") },
		"cut off": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"choices":[{"finish_reason":"length","message":{"content":"{\"translations\":[\"x\"]}"}}]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			var logs strings.Builder
			s := New(providerFor(t, srv.URL+"/v1/chat?token=URL-SECRET"), time.Second, slog.New(slog.NewJSONHandler(&logs, nil)))
			loc := s.Localize(mk("j", "finalizer", "fr", ""), "ar")
			require.Equal(t, StatusUnavailable, loc.FinalAssessment.UserText.Status)
			b, _ := json.Marshal(loc)
			for _, leak := range []string{apiKey, "tenant-SECRET", "URL-SECRET", srv.URL, "PROVIDER-MSG", "Le texte", "Translate every"} {
				require.NotContains(t, string(b), leak)
				require.NotContains(t, logs.String(), leak)
			}
			require.Contains(t, logs.String(), `"job_id":"j"`)
		})
	}
	// connection refused: the *url.Error names the URL
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, err := providerFor(t, url).Translate(context.Background(), "ar", []string{"x"})
	require.EqualError(t, err, "request failed")
}

func TestFromRegistryWithoutTranslation(t *testing.T) {
	require.Nil(t, FromRegistry(load(t, "analyzers:\n  - {id: text_stats, name: Stats, category: stats, execution: {adapter: custom, handler: text_stats}}\n")))
}

func load(t *testing.T, yaml string) *registry.Registry {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.yaml")
	require.NoError(t, os.WriteFile(p, []byte(yaml), 0o600))
	reg, err := registry.Load(p, registry.Options{HandlerExists: func(string) bool { return true }})
	require.NoError(t, err)
	return reg
}
