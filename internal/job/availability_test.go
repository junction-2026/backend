package job

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func skipped(id, reason string, deps ...string) AnalyzerRun {
	return AnalyzerRun{ID: id, Status: RunSkipped, Reason: Str(reason), DependsOn: deps}
}

func TestAvailabilityTable(t *testing.T) {
	const (
		claims  = "Not run: the text contains no specific factual claims to check."
		topics  = "Not run: the text names no concrete topics or events to search news coverage for."
		sources = "Not run: there were no news articles or page URL to look up."
		input   = "Not run: this analyzer does not apply to this kind of input."
		request = "Not run: this analyzer was not requested for this job."
	)
	cases := []struct {
		name  string
		run   AnalyzerRun
		avail string // "" = null
		note  string // "" = null
	}{
		{"succeeded", AnalyzerRun{Status: RunSucceeded}, AvailRan, ""},
		{"pending", AnalyzerRun{Status: RunPending}, "", ""},
		{"running", AnalyzerRun{Status: RunRunning}, "", ""},
		{"no_checkable_claims", skipped("a", "no_checkable_claims"), AvailNotApplicable, claims},
		{"no_searchable_topics", skipped("a", "no_searchable_topics"), AvailNotApplicable, topics},
		{"no_sources", skipped("a", "no_sources"), AvailNotApplicable, sources},
		{"not_applicable", skipped("a", "not_applicable"), AvailNotApplicable, input},
		{"not_selected", skipped("a", "not_selected"), AvailNotApplicable, request},
		{"disabled", skipped("a", "disabled"), AvailNotApplicable, request},
		{"dependency_failed", skipped("a", "dependency_failed"), AvailUnavailable, NoteUnavailable},
		{"dependency_skipped without skipped dependency", skipped("a", "dependency_skipped"), AvailUnavailable, NoteUnavailable},
		{"cancelled", skipped("a", "cancelled"), AvailUnavailable, NoteUnavailable},
		{"input_unavailable", skipped("a", "input_unavailable"), AvailUnavailable, NoteUnavailable},
		{"not_configured", skipped("a", "not_configured"), AvailUnavailable, NoteUnavailable},
		{"no_text_content", skipped("a", "no_text_content"), AvailUnavailable, NoteUnavailable},
		{"unknown reason", skipped("a", "something_new"), AvailUnavailable, NoteUnavailable},
	}
	for _, code := range []string{CodeTimeout, CodeAdapterError, CodeInvalidOutput, CodeCancelled} {
		cases = append(cases, struct {
			name  string
			run   AnalyzerRun
			avail string
			note  string
		}{"failed " + code, AnalyzerRun{Status: RunFailed, Error: &RunError{Code: code}}, AvailUnavailable, NoteUnavailable})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, n := Availability(&c.run, []AnalyzerRun{c.run})
			if c.avail == "" {
				require.Nil(t, a)
			} else {
				require.Equal(t, c.avail, *a)
			}
			if c.note == "" {
				require.Nil(t, n)
			} else {
				require.Equal(t, c.note, *n)
			}
		})
	}
}

func TestAvailabilityDependencySkipped(t *testing.T) {
	runs := []AnalyzerRun{
		{ID: "ok", Status: RunSucceeded},
		skipped("ns", "no_searchable_topics"),
		skipped("fc", "no_checkable_claims"),
		skipped("si", "dependency_skipped", "ok", "ns", "fc"), // first skipped in depends_on order: ns
		skipped("top", "dependency_skipped", "si"),
	}
	a, n := Availability(&runs[4], runs)
	require.Equal(t, AvailNotApplicable, *a)
	require.Equal(t, notApplicableNotes["no_searchable_topics"], *n)

	// chain: c0 is the root, c_i depends on c_{i-1}.
	chain := func(length int) []AnalyzerRun {
		rs := []AnalyzerRun{skipped("c0", "no_sources")}
		for i := 1; i <= length; i++ {
			rs = append(rs, skipped(fmt.Sprint("c", i), "dependency_skipped", fmt.Sprint("c", i-1)))
		}
		return rs
	}
	rs := chain(8)
	a, _ = Availability(&rs[8], rs)
	require.Equal(t, AvailNotApplicable, *a, "8 steps reach the root")
	rs = chain(9)
	a, n = Availability(&rs[9], rs)
	require.Equal(t, AvailUnavailable, *a, "more than 8 steps")
	require.Equal(t, NoteUnavailable, *n)
}
