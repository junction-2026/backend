package job

// Event-stream state of a job (analysis-job-events). It lives on Record, never on Job: GET /jobs/{id} does not change.

// PlanEntry is one planned analyzer (or the finalizer) of the run plan.
type PlanEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ActivityKey string   `json:"activity_key"`
	DependsOn   []string `json:"depends_on"`
	Phase       int      `json:"phase"`
}

// Plan is the data of analysis.planned. It never changes once set.
type Plan struct {
	Analyzers []PlanEntry `json:"analyzers"`
	Total     int         `json:"total"`
}

// Progress is the progress counter carried by events. A value is never modified after it was published.
type Progress struct {
	Done    int      `json:"done"`
	Running []string `json:"running"`
	Total   int      `json:"total"`
}

// Activity says in user terms what is being worked on.
type Activity struct {
	Key    string         `json:"key"`
	Text   string         `json:"text"`
	Detail *string        `json:"detail"`
	Params map[string]any `json:"params"`
}

// Snapshot of the event-stream state: copies that are safe to marshal after the lock is released.
type Snapshot struct {
	Plan       *Plan
	Progress   *Progress
	Previews   map[string]any
	Activities map[string]Activity
}
