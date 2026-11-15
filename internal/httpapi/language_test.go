package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/job"
)

// inLanguage gives j a translation run with the status and raw result ("" for none).
func inLanguage(j job.Job, status, result string) job.Job {
	run := job.AnalyzerRun{ID: "translation", Status: status}
	if result != "" {
		run.Result = json.RawMessage(result)
	}
	j.Analyzers = append(j.Analyzers, run)
	return j
}

func languages(p page) map[string]*string {
	out := map[string]*string{}
	for _, it := range p.Items {
		out[it.ID] = it.Language
	}
	return out
}

func TestListJobsLanguage(t *testing.T) {
	private := inLanguage(withContent(mkJob("de-private", 9, job.StatusCompleted), "needle"), job.RunSucceeded, `{"source_language":"de"}`)
	private.Public = false
	x := persistedHarness(t,
		inLanguage(withContent(mkJob("de-done", 0, job.StatusCompleted), "needle"), job.RunSucceeded, `{"source_language":"de"}`),
		inLanguage(withContent(mkJob("de-failed", 1, job.StatusFailed), "needle"), job.RunSucceeded, `{"source_language":"de"}`),
		inLanguage(withContent(mkJob("fr-failed", 2, job.StatusFailed), "needle"), job.RunSucceeded, `{"source_language":"fr"}`),
		inLanguage(withContent(mkJob("unk-word", 3, job.StatusCompleted), "x"), job.RunSucceeded, `{"source_language":"german"}`),
		inLanguage(withContent(mkJob("unk-null", 4, job.StatusCompleted), "x"), job.RunSucceeded, `{"source_language":null}`),
		inLanguage(withContent(mkJob("unk-failed", 5, job.StatusFailed), "x"), job.RunFailed, `{"source_language":"de"}`),
		inLanguage(withContent(mkJob("unk-skipped", 6, job.StatusCompleted), "x"), job.RunSkipped, ""),
		inLanguage(withContent(mkJob("unk-running", 7, job.StatusRunning), "x"), job.RunRunning, ""),
		withContent(mkJob("unk-none", 8, job.StatusCompleted), "x"),
		inLanguage(withContent(mkJob("ru-done", 10, job.StatusCompleted), "x"), job.RunSucceeded, `{"source_language":" RU "}`),
		private,
	)
	all := list(t, x, "")
	require.Len(t, all.Items, 10, "the non-public job is never listed")
	l := languages(all)
	require.Equal(t, "de", *l["de-done"])
	require.Equal(t, "de", *l["de-failed"])
	require.Equal(t, "ru", *l["ru-done"], "normalized code")
	require.Equal(t, "fr", *l["fr-failed"])
	for _, id := range []string{"unk-word", "unk-null", "unk-failed", "unk-skipped", "unk-running", "unk-none"} {
		require.Nil(t, l[id], id)
	}
	require.Contains(t, x.do("GET", "/api/v1/jobs?q=unk-none", "").Body.String(), `"language":null`, "always present")

	for q, want := range map[string][]string{
		"de":                           {"de-done", "de-failed"},
		"ru":                           {"ru-done"},
		"%20DE%20":                     {"de-done", "de-failed"},
		"Ru":                           {"ru-done"},
		"unknown":                      {"unk-word", "unk-null", "unk-failed", "unk-skipped", "unk-running", "unk-none"},
		"UNKNOWN":                      {"unk-word", "unk-null", "unk-failed", "unk-skipped", "unk-running", "unk-none"},
		"zz":                           {},
		"fr&q=needle&status=failed":    {"fr-failed"},
		"de&q=needle&status=failed":    {"de-failed"},
		"unknown&q=x&status=completed": {"unk-word", "unk-null", "unk-skipped", "unk-none"},
		"unknown&status=running":       {"unk-running"},
	} {
		p := list(t, x, "?language="+q)
		require.Equal(t, want, ids(p), "language=%s", q)
		require.Nil(t, p.NextCursor)
	}
	body := func(q string) string { return x.do("GET", "/api/v1/jobs"+q, "").Body.String() }
	require.Equal(t, body(""), body("?language=%20"), "blank does not filter")
	require.Equal(t, body(""), body("?language="))
	require.Equal(t, body("?language=de"), body("?language=%20DE%20"))
	for _, v := range []string{"deu", "de-AT", "german", "d", "null", "1x", "d%20e", "unknownx"} {
		t.Run(v, func(t *testing.T) { wantError(t, x.do("GET", "/api/v1/jobs?language="+v, ""), 400, "invalid_request") })
	}
}

func TestListJobsLanguagePaging(t *testing.T) {
	var jobs []job.Job
	for i := 0; i < 30; i++ {
		src := `{"source_language":"en"}`
		if i == 4 || i == 17 || i == 29 {
			src = `{"source_language":"de"}`
		}
		jobs = append(jobs, inLanguage(withContent(mkJob(fmt.Sprintf("j%02d", i), i, job.StatusCompleted), "plain"), job.RunSucceeded, src))
	}
	x := persistedHarness(t, jobs...)
	p := list(t, x, "?language=de&limit=2")
	require.Equal(t, []string{"j04", "j17"}, ids(p))
	require.NotNil(t, p.NextCursor)
	p = list(t, x, "?language=de&limit=2&cursor="+*p.NextCursor)
	require.Equal(t, []string{"j29"}, ids(p))
	require.Nil(t, p.NextCursor)
	p = list(t, x, "?language=de&q=plain&limit=3")
	require.Equal(t, []string{"j04", "j17", "j29"}, ids(p))
	require.Nil(t, p.NextCursor, "exactly limit matches: no next page")
}
