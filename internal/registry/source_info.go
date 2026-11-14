package registry

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/junction-2026/backend/internal/outlets"
)

// AdapterSourceInfo looks up the outlet, political leaning and country behind news_search articles and the job's source URL.
const AdapterSourceInfo = "source_info"

// DefaultOutletsFile is where the image ships data/outlets.csv.
const DefaultOutletsFile = "/etc/orchestrator/data/outlets.csv"

// checkSourceInfo validates the source_info settings and rejects outlets_file on other adapters.
func (a *Analyzer) checkSourceInfo() error {
	e := &a.Execution
	if e.Adapter != AdapterSourceInfo {
		if e.OutletsFile != "" {
			return fmt.Errorf("execution.outlets_file is only allowed for adapter %s", AdapterSourceInfo)
		}
		return nil
	}
	only := *e
	only.Adapter, only.Timeout, only.OutletsFile = "", 0, ""
	if !reflect.ValueOf(only).IsZero() { // every other execution key belongs to another adapter
		return fmt.Errorf("only execution.outlets_file and execution.timeout are allowed for adapter %s", e.Adapter)
	}
	if strings.TrimSpace(e.OutletsFile) == "" {
		e.OutletsFile = DefaultOutletsFile
	}
	if a.Trigger != TriggerOnDeps {
		return fmt.Errorf("adapter %s requires trigger %s", e.Adapter, TriggerOnDeps)
	}
	if !strings.HasPrefix(a.Input.Bindings["search"], "$analyzers.") {
		return fmt.Errorf("input.bindings.search must be $analyzers.<news_search analyzer id> for adapter %s", e.Adapter)
	}
	for name, src := range a.Input.Bindings {
		if name != "search" && !(name == "source" && src == "$input.source") {
			return fmt.Errorf("input.bindings for adapter %s accept only search: $analyzers.<id> and source: $input.source", e.Adapter)
		}
	}
	return nil
}

// outletTable loads path once per Load call; analyzers with the same file share the table.
func (o *Options) outletTable(path string) (*outlets.Table, error) {
	if t := o.outlets[path]; t != nil {
		return t, nil
	}
	t, err := outlets.Load(path)
	if err != nil {
		return nil, err
	}
	if o.outlets != nil {
		o.outlets[path] = t
	}
	return t, nil
}

// checkSourceInfoGraph: the bound search analyzer must run the news_search adapter.
func (r *Registry) checkSourceInfoGraph() error {
	for _, a := range r.all {
		if a.Execution.Adapter != AdapterSourceInfo {
			continue
		}
		id := strings.TrimPrefix(a.Input.Bindings["search"], "$analyzers.")
		if dep := r.byID[id]; dep == nil || dep.Execution.Adapter != AdapterNewsSearch {
			return a.errf("input.bindings.search: %q is not an analyzer with adapter %s", id, AdapterNewsSearch)
		}
	}
	return nil
}
