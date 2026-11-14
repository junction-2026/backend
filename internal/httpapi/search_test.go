package httpapi

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/store"
)

func withContent(j job.Job, content string) job.Job {
	j.Input = &job.Input{Content: content, ContentType: "text/plain"}
	return j
}

// persistedHarness stores the jobs in a SQLite history, then serves them from a fresh engine (as after a restart).
func persistedHarness(t *testing.T, jobs ...job.Job) *harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	st, err := store.OpenSQLite(path)
	require.NoError(t, err)
	for _, j := range jobs {
		r := &job.Record{Job: j}
		r.Lock()
		require.NoError(t, st.Save(r))
		r.Unlock()
	}
	require.NoError(t, st.Close())
	st, err = store.OpenSQLite(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	reg := loadReg(t, regYAMLText, map[string]string{"FINALIZER_URL": "http://finalizer.invalid", "FINALIZER_TOKEN": "t"})
	eng := engine.New(reg, engine.Config{Store: st})
	t.Cleanup(eng.Close)
	return &harness{h: NewHandler(Config{Service: eng, Registry: reg, Logger: slog.New(slog.DiscardHandler)})}
}

func ids(p page) []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestListJobsSearch(t *testing.T) {
	scrape := mkJob("job-scrape-7f3a", 3, job.StatusFailed)
	scrape.Scrape = &job.Scrape{Status: job.RunFailed, ScrapeRequest: job.ScrapeRequest{URL: "https://example.com/news/42"}}
	hidden := withContent(mkJob("job-hidden-9c1d", 4, job.StatusCompleted), "a needle in private")
	hidden.Public = false
	x := persistedHarness(t,
		withContent(mkJob("job-a", 0, job.StatusCompleted), "The Minister said X"),
		withContent(mkJob("job-b", 1, job.StatusCompleted), "Weather today"),
		withContent(mkJob("job-c", 2, job.StatusRunning), "ÄRZTE warnen 100 % sicher"),
		scrape, hidden,
		withContent(mkJob("job-d", 5, job.StatusFailed), "a needle that failed"),
		withContent(mkJob("job-e", 6, job.StatusCompleted), "a needle that completed"),
	)
	for q, want := range map[string][]string{
		"minister":             {"job-a"},
		"MINISTER":             {"job-a"},
		"crape-7f":             {"job-scrape-7f3a"},
		"example.com/news":     {"job-scrape-7f3a"},
		"%C3%A4rzte":           {"job-c"}, // ärzte
		"%25":                  {"job-c"}, // % is literal
		"_":                    {},
		"a%25":                 {},
		"needle":               {"job-d", "job-e"},
		"needle&status=failed": {"job-d"},
		"job-hidden-9c1d":      {},
		"%20%20needle%20":      {"job-d", "job-e"},
	} {
		p := list(t, x, "?q="+q)
		require.Equal(t, want, ids(p), "q=%s", q)
		require.Nil(t, p.NextCursor)
	}
	require.Equal(t, x.do("GET", "/api/v1/jobs", "").Body.String(), x.do("GET", "/api/v1/jobs?q=%20%20", "").Body.String())
	require.Len(t, list(t, x, "").Items, 6, "the non-public job is never listed")

	ok := strings.Repeat("ä", 200)
	require.Equal(t, 200, x.do("GET", "/api/v1/jobs?q=%20"+ok+"%20", "").Code, "200 code points after trimming")
	wantError(t, x.do("GET", "/api/v1/jobs?q="+ok+"x", ""), 400, "invalid_request")
}

func TestListJobsSearchPaging(t *testing.T) {
	var jobs []job.Job
	for i := 0; i < 25; i++ {
		c := "plain"
		if i == 24 {
			c = "the needle"
		}
		jobs = append(jobs, withContent(mkJob(fmt.Sprintf("j%02d", i), i, job.StatusCompleted), c))
	}
	x := persistedHarness(t, jobs...)
	p := list(t, x, "?q=needle&limit=20")
	require.Equal(t, []string{"j24"}, ids(p))
	require.Nil(t, p.NextCursor)

	jobs = nil
	for i := 0; i < 30; i++ {
		c := "plain"
		if i == 4 || i == 17 || i == 29 {
			c = "needle " + c
		}
		jobs = append(jobs, withContent(mkJob(fmt.Sprintf("j%02d", i), i, job.StatusCompleted), c))
	}
	x = persistedHarness(t, jobs...)
	p = list(t, x, "?q=needle&limit=2")
	require.Equal(t, []string{"j04", "j17"}, ids(p))
	require.NotNil(t, p.NextCursor)
	p = list(t, x, "?q=needle&limit=2&cursor="+*p.NextCursor)
	require.Equal(t, []string{"j29"}, ids(p))
	require.Nil(t, p.NextCursor)
}

func TestJobSummaryExcerpt(t *testing.T) {
	long := strings.Repeat("ü", 199) + "😀" + strings.Repeat("x", 300) // 500 code points, the 200th is multi-byte
	scrape := mkJob("scrape", 1, job.StatusFailed)
	scrape.Scrape = &job.Scrape{Status: job.RunFailed, ScrapeRequest: job.ScrapeRequest{URL: "https://bsky.app/profile/alice.bsky.social/post/3kxyz"}}
	longURL := mkJob("longurl", 2, job.StatusRunning)
	longURL.Scrape = &job.Scrape{Status: job.RunPending, ScrapeRequest: job.ScrapeRequest{URL: "https://e.x/" + strings.Repeat("a", 300)}}
	x := persistedHarness(t, // stored without an excerpt member, served after a restart
		withContent(mkJob("short", 0, job.StatusCompleted), "  Some article text\n"),
		scrape, longURL,
		withContent(mkJob("long", 3, job.StatusCompleted), long),
		mkJob("none", 4, job.StatusQueued),
	)
	got := map[string]*string{}
	for _, it := range list(t, x, "").Items {
		got[it.ID] = it.Excerpt
	}
	require.Equal(t, "  Some article text\n", *got["short"], "verbatim")
	require.Equal(t, "https://bsky.app/profile/alice.bsky.social/post/3kxyz", *got["scrape"])
	require.Equal(t, ("https://e.x/" + strings.Repeat("a", 300))[:200], *got["longurl"])
	require.Equal(t, strings.Repeat("ü", 199)+"😀", *got["long"])
	require.Nil(t, got["none"])
	require.Contains(t, x.do("GET", "/api/v1/jobs?q=none", "").Body.String(), `"excerpt":null`)
}
