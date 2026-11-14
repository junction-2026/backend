package job

// Availability values of a settled AnalyzerRun.
const (
	AvailRan           = "ran"
	AvailNotApplicable = "not_applicable"
	AvailUnavailable   = "unavailable"
)

// Capability-specific skip reasons (fact_check, news_search, source_info).
const (
	ReasonNoCheckableClaims  = "no_checkable_claims"
	ReasonNoSearchableTopics = "no_searchable_topics"
	ReasonNoSources          = "no_sources"
)

// NoteUnavailable is the note of every unavailable run.
const NoteUnavailable = "Unavailable: the analyzer failed or is not set up, so its result is missing."

var notApplicableNotes = map[string]string{
	ReasonNoCheckableClaims:  "Not run: the text contains no specific factual claims to check.",
	ReasonNoSearchableTopics: "Not run: the text names no concrete topics or events to search news coverage for.",
	ReasonNoSources:          "Not run: there were no news articles or page URL to look up.",
	ReasonNotApplicable:      "Not run: this analyzer does not apply to this kind of input.",
	ReasonNotSelected:        "Not run: this analyzer was not requested for this job.",
	ReasonDisabled:           "Not run: this analyzer was not requested for this job.",
}

// maxDependencySteps bounds dependency_skipped root resolution.
const maxDependencySteps = 8

// Availability maps a run to its availability and note (analyzer-run-availability).
// runs are the job's AnalyzerRuns, used to resolve dependency_skipped to its
// root skip. Unsettled runs yield nil, nil.
func Availability(run *AnalyzerRun, runs []AnalyzerRun) (availability, note *string) {
	for step := 0; ; step++ {
		switch {
		case run.Status == RunSucceeded:
			return Str(AvailRan), nil
		case run.Status != RunFailed && run.Status != RunSkipped:
			return nil, nil
		case run.Status == RunSkipped && run.Reason != nil:
			if n, ok := notApplicableNotes[*run.Reason]; ok {
				return Str(AvailNotApplicable), Str(n)
			}
			if *run.Reason == ReasonDependencySkipped && step < maxDependencySteps {
				if dep := firstSkippedDep(run, runs); dep != nil {
					run = dep
					continue
				}
			}
		}
		return Str(AvailUnavailable), Str(NoteUnavailable)
	}
}

// firstSkippedDep returns the first run in run.DependsOn order that is skipped.
func firstSkippedDep(run *AnalyzerRun, runs []AnalyzerRun) *AnalyzerRun {
	for _, id := range run.DependsOn {
		for i := range runs {
			if runs[i].ID == id && runs[i].Status == RunSkipped {
				return &runs[i]
			}
		}
	}
	return nil
}
