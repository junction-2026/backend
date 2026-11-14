package adapter

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/outlets"
	"github.com/junction-2026/backend/internal/registry"
)

// ReasonNoSources is the skip reason of source_info: news_search found no articles and the job has no source URL.
const ReasonNoSources = "no_sources"

// sourceArticle is the shared SourceArticle object.
type sourceArticle struct {
	URL     string          `json:"url"`
	Domain  *string         `json:"domain"`
	Matched bool            `json:"matched"`
	Match   string          `json:"match,omitempty"`
	Outlet  *outlets.Outlet `json:"outlet,omitempty"`
}

// sourceInfo matches the articles of the bound news_search result and the job's source URL against the outlet table.
func (x *Executor) sourceInfo(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	if err := ContextError(ctx); err != nil {
		return nil, err
	}
	table := def.Execution.Outlets
	if table == nil {
		return nil, adapterErr("the outlet table is not loaded")
	}
	b, _ := json.Marshal(req.Inputs["search"])
	var in struct {
		Articles *[]struct {
			URL string `json:"url"`
		} `json:"articles"`
	}
	if json.Unmarshal(b, &in) != nil || in.Articles == nil {
		return nil, adapterErr("the news_search result is not a NewsSearch object")
	}
	src, _ := req.Inputs["source"].(job.Source)
	pageURL := strings.TrimSpace(src.URL)
	if len(*in.Articles) == 0 && pageURL == "" {
		return nil, &Skip{Reason: ReasonNoSources}
	}

	articles := make([]sourceArticle, 0, len(*in.Articles))
	bias, countries := map[string]int{}, map[string]int{}
	distinct := map[*outlets.Outlet]bool{}
	matched := 0
	for _, a := range *in.Articles {
		sa := sourceArticle{URL: a.URL}
		if host := outlets.Host(a.URL); host != "" {
			sa.Domain = &host
			if o, how := table.Match(host); o != nil {
				sa.Matched, sa.Match, sa.Outlet = true, how, o
				matched++
				bias[o.Bias]++
				countries[o.CountryCode]++
				distinct[o] = true
			}
		}
		articles = append(articles, sa)
	}
	var page *outlets.Outlet
	if host := outlets.Host(pageURL); host != "" {
		page, _ = table.Match(host)
	}
	if err := ContextError(ctx); err != nil {
		return nil, err
	}
	out, err := json.Marshal(map[string]any{
		"articles":    articles,
		"page_outlet": page,
		"summary": map[string]any{"articles": len(articles), "matched": matched, "bias": bias,
			"countries": countries, "outlets": len(distinct)},
		"table": map[string]any{"outlets": table.Count, "source": table.Source},
	})
	if err != nil {
		return nil, adapterErr("encoding the result: %v", err)
	}
	return out, nil
}
