package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/localization"
	"github.com/junction-2026/backend/internal/store"
)

// fakeTr prefixes texts with the language, or fails with a provider-looking message.
type fakeTr struct {
	calls atomic.Int32
	fail  bool
}

func (f *fakeTr) Translate(_ context.Context, lang string, in []string) ([]string, error) {
	f.calls.Add(1)
	if f.fail {
		return nil, errors.New("provider HTTP 503")
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = lang + ":" + s
	}
	return out, nil
}

// doneJob is a French job with a finalizer user_text and two LLM findings.
func doneJob(id string) job.Job {
	j := mkJob(id, 0, job.StatusCompleted)
	j.Analyzers = []job.AnalyzerRun{
		{ID: "translation", Status: job.RunSucceeded, Result: json.RawMessage(`{"source_language":"fr","translated_text":"Lies."}`)},
		{ID: "llm_techniques", Status: job.RunSucceeded, Result: json.RawMessage(`{"explanation_language":"fr","findings":[` +
			`{"label":"misleading_headline","text":"« mensonges »","explanation":"Le titre exagère."},` +
			`{"label":"statistical_cherry_picking","text":"10 %","explanation":"Un seul chiffre."}]}`)},
	}
	j.Finalization = job.Finalization{Status: job.RunSucceeded,
		Result: json.RawMessage(`{"verdict":"detected","confidence":0.8,"user_text":"Le texte est trompeur.","produced_by":"finalizer"}`)}
	return j
}

func locHarness(t *testing.T, tr localization.Translator) *harness {
	t.Helper()
	x := newHarness(t)
	log := slog.New(slog.NewJSONHandler(x.logs, nil))
	x.h = NewHandler(Config{Service: x.svc, Registry: loadReg(t, regYAMLText, map[string]string{"FINALIZER_URL": "http://finalizer.invalid", "FINALIZER_TOKEN": "t"}), Localizer: localization.New(tr, time.Second, log), Logger: log})
	return x
}

func TestLocalizationArabic(t *testing.T) {
	f := &fakeTr{}
	x := locHarness(t, f)
	x.svc.jobs["j1"] = doneJob("j1")
	rec := x.do("GET", "/api/v1/jobs/j1/localization?language=ar", "")
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"job_id":"j1","language":"ar",
		"final_assessment":{"user_text":{"status":"translated","text":"ar:Le texte est trompeur.","source_language":"fr"}},
		"llm_findings":[
			{"index":0,"label":"misleading_headline","explanation":{"status":"translated","text":"ar:Le titre exagère.","source_language":"fr"}},
			{"index":1,"label":"statistical_cherry_picking","explanation":{"status":"translated","text":"ar:Un seul chiffre.","source_language":"fr"}}]}`,
		rec.Body.String())
	require.NotContains(t, rec.Body.String(), "mensonges", "span text is never sent or returned")

	again := x.do("GET", "/api/v1/jobs/j1/localization?language=ar", "")
	require.Equal(t, rec.Body.String(), again.Body.String())
	require.EqualValues(t, 1, f.calls.Load(), "second request served from the cache")

	rec = x.do("GET", "/api/v1/jobs/j1/localization?language=FR-ca", "")
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"job_id":"j1","language":"fr",
		"final_assessment":{"user_text":{"status":"original","text":"Le texte est trompeur.","source_language":"fr"}},
		"llm_findings":[
			{"index":0,"label":"misleading_headline","explanation":{"status":"original","text":"Le titre exagère.","source_language":"fr"}},
			{"index":1,"label":"statistical_cherry_picking","explanation":{"status":"original","text":"Un seul chiffre.","source_language":"fr"}}]}`,
		rec.Body.String())
	require.EqualValues(t, 1, f.calls.Load(), "source language: no provider call")
}

func TestLocalizationErrors(t *testing.T) {
	x := locHarness(t, &fakeTr{})
	x.svc.jobs["done"] = doneJob("done")
	x.svc.jobs["running"] = mkJob("running", 0, job.StatusRunning)
	for _, c := range []struct {
		path, code string
		status     int
	}{
		{"/api/v1/jobs/nope/localization?language=ar", "not_found", 404},
		{"/api/v1/jobs/running/localization?language=ar", "job_not_finished", 409},
		{"/api/v1/jobs/done/localization", "invalid_request", 400},
		{"/api/v1/jobs/done/localization?language=", "invalid_request", 400},
		{"/api/v1/jobs/done/localization?language=xx", "unsupported_language", 400},
		{"/api/v1/jobs/done/localization?language=es-ES", "unsupported_language", 400},
	} {
		e := wantError(t, x.do("GET", c.path, ""), c.status, c.code)
		if c.code == "unsupported_language" {
			require.Equal(t, []any{"en", "fr", "ar", "de", "fi", "ru"}, e.Error.Details["supported"])
		}
	}
	rec := x.do("POST", "/api/v1/jobs/done/localization?language=ar", "")
	wantError(t, rec, 405, "method_not_allowed")
	require.Equal(t, "GET", rec.Header().Get("Allow"))

	rec = x.do("GET", "/api/v1/jobs/done/localization?language=DE-at", "")
	require.Equal(t, 200, rec.Code)
	var got struct{ Language string }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "de", got.Language)
}

func TestLocalizationOrchestratorFixedText(t *testing.T) {
	f := &fakeTr{}
	x := locHarness(t, f)
	j := mkJob("o", 0, job.StatusCompleted)
	j.Finalization = job.Finalization{Status: job.RunSucceeded,
		Result: json.RawMessage(`{"verdict":"uncertain","confidence":null,"user_text":"No analyzer produced a usable result, so no verdict can be given.","produced_by":"orchestrator"}`)}
	x.svc.jobs["o"] = j
	rec := x.do("GET", "/api/v1/jobs/o/localization?language=fi", "")
	require.JSONEq(t, `{"job_id":"o","language":"fi","llm_findings":[],
		"final_assessment":{"user_text":{"status":"translated","source_language":"en","text":"Mikään analyysi ei tuottanut käyttökelpoista tulosta, joten arviota ei voida antaa."}}}`,
		rec.Body.String())
	require.Zero(t, f.calls.Load())
}

func TestLocalizationProviderFailure(t *testing.T) {
	for name, tr := range map[string]localization.Translator{"missing": nil, "failing": &fakeTr{fail: true}} {
		t.Run(name, func(t *testing.T) {
			x := locHarness(t, tr)
			x.svc.jobs["j1"] = doneJob("j1")
			rec := x.do("GET", "/api/v1/jobs/j1/localization?language=ar", "")
			require.Equal(t, 200, rec.Code)
			var got localization.JobLocalization
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, localization.LocalizedText{Status: "unavailable", SourceLanguage: job.Ptr("fr")}, got.FinalAssessment.UserText)
			for _, f := range got.LLMFindings {
				require.Equal(t, "unavailable", f.Explanation.Status)
				require.Nil(t, f.Explanation.Text)
			}
			require.NotContains(t, rec.Body.String(), "503")
			require.NotContains(t, x.logs.String(), "Le texte", "no texts in logs")
		})
	}
}

// The job and its event stream are untouched by localization requests.
func TestLocalizationIsReadOnly(t *testing.T) {
	x := locHarness(t, &fakeTr{})
	x.svc.bus = events.New(16)
	x.svc.jobs["j1"] = doneJob("j1")
	sub := x.svc.bus.Subscribe("j1", func() any { return nil })
	defer sub.Close()
	<-sub.Events() // the snapshot

	before := x.do("GET", "/api/v1/jobs/j1", "").Body.String()
	stored := doneJob("j1") // an independent deep copy of what the service holds
	for _, l := range []string{"ar", "de", "fi", "ar", "ru-RU"} {
		require.Equal(t, 200, x.do("GET", "/api/v1/jobs/j1/localization?language="+l, "").Code)
	}
	require.Equal(t, before, x.do("GET", "/api/v1/jobs/j1", "").Body.String())
	require.Equal(t, stored, x.svc.jobs["j1"])
	select {
	case ev := <-sub.Events():
		t.Fatalf("unexpected event %s", ev.Type)
	case <-time.After(20 * time.Millisecond):
	}
}

// A done job that is only in the SQLite store (as after a restart) is localized and stays unchanged there.
func TestLocalizationOfPersistedJob(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "jobs.db"))
	require.NoError(t, err)
	defer st.Close()
	rec := &job.Record{Job: doneJob("p1"), LastSeq: 7}
	rec.Lock()
	require.NoError(t, st.Save(rec))
	rec.Unlock()

	reg := loadReg(t, regYAMLText, map[string]string{"FINALIZER_URL": "http://finalizer.invalid", "FINALIZER_TOKEN": "t"})
	eng := engine.New(reg, engine.Config{Store: st})
	defer eng.Close()
	f := &fakeTr{}
	h := NewHandler(Config{Service: eng, Registry: reg, Localizer: localization.New(f, time.Second, nil), Logger: slog.New(slog.DiscardHandler)})
	x := &harness{h: h}

	loaded, _, err := st.Load("p1")
	require.NoError(t, err)
	before := x.do("GET", "/api/v1/jobs/p1", "").Body.String()
	r := x.do("GET", "/api/v1/jobs/p1/localization?language=ar", "")
	require.Equal(t, 200, r.Code, r.Body.String())
	require.Contains(t, r.Body.String(), `"text":"ar:Le texte est trompeur."`)
	require.EqualValues(t, 1, f.calls.Load())

	require.Equal(t, before, x.do("GET", "/api/v1/jobs/p1", "").Body.String())
	after, _, err := st.Load("p1")
	require.NoError(t, err)
	require.Equal(t, loaded.Job, after.Job)
	require.Equal(t, int64(7), after.LastSeq, "no event emitted")
}
