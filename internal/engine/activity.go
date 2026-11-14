package engine

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/junction-2026/backend/internal/adapter"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Activity descriptions (analysis-job-events "Activity object"). Texts are fixed English from this table; facts
// come from the adapters' own decision functions (internal/adapter/activity_facts.go). No LLM, translation or
// network call is made here.

const (
	keyScrape    = "scrape"
	keyFinalizer = "finalizer"
)

var activityTexts = map[string]string{
	"translation":          "Translating the text",
	"propaganda_detection": "Checking for propaganda",
	"techniques":           "Detecting persuasion techniques",
	"llm_techniques":       "Looking for misleading framing",
	"topic_extraction":     "Finding topics and claims",
	"news_search":          "Searching news coverage",
	"fact_check":           "Looking up fact-checks",
	"source_info":          "Checking the news outlets",
	keyFinalizer:           "Writing the verdict",
	keyScrape:              "Fetching the content",
}

// languageNames names the languages news_search detail may mention; other codes are shown as the code.
var languageNames = map[string]string{
	"de": "German", "en": "English", "es": "Spanish", "fi": "Finnish", "fr": "French", "it": "Italian",
	"nl": "Dutch", "pl": "Polish", "pt": "Portuguese", "ru": "Russian", "sv": "Swedish", "uk": "Ukrainian",
}

func newActivity(key, fallbackText string) job.Activity {
	text, ok := activityTexts[key]
	if !ok {
		text = fallbackText
	}
	return job.Activity{Key: key, Text: text, Params: map[string]any{}}
}

// activityFor builds the Activity of analyzer def about to run with inputs. It never fails: a fact that cannot be
// determined is left out of params and detail stays null.
func activityFor(def *registry.Analyzer, inputs map[string]any) (act job.Activity) {
	key := def.ActivityKey()
	act = newActivity(key, def.Name)
	defer func() {
		if recover() != nil {
			act = newActivity(key, def.Name)
		}
	}()
	set := func(detail string, k string, v any) { act.Detail, act.Params[k] = &detail, v }
	switch ad := def.Execution.Adapter; {
	case key == "news_search" && ad == registry.AdapterNewsSearch:
		if langs, ok := adapter.NewsSearchLanguages(def, inputs); ok {
			names := make([]string, len(langs))
			for i, l := range langs {
				names[i] = l
				if n, ok := languageNames[l]; ok {
					names[i] = n
				}
			}
			set("in "+joinAnd(names), "languages", langs)
		}
	case key == "fact_check" && ad == registry.AdapterFactCheck:
		if n, ok := adapter.FactCheckClaims(def, inputs); ok {
			d := "for " + strconv.Itoa(n) + " claims"
			if n == 1 {
				d = "for 1 claim"
			}
			set(d, "claims", n)
		}
	case key == "techniques" && ad == registry.AdapterTechniqueClassifier:
		if models, ok := adapter.TechniqueModels(def, inputs); ok {
			names := make([]string, len(models))
			for i, m := range models {
				names[i] = map[string]string{"nci-social": "short-post model", "nci-technique": "document model"}[m]
			}
			set(joinAnd(names), "models", models)
		}
	case key == "llm_techniques" && ad == registry.AdapterLLMTechniques:
		checked := adapter.LLMTitleChecked(inputs)
		act.Params["title_checked"] = checked
		if checked {
			act.Detail = job.Ptr("including the headline")
		}
	}
	return act
}

// joinAnd renders "A", "A and B" or "A, B and C".
func joinAnd(s []string) string {
	if len(s) < 2 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// scrapeActivity describes the scrape of rawURL: the lower-case host without a leading "www.", when it has one.
func scrapeActivity(rawURL string) job.Activity {
	act := newActivity(keyScrape, "Scrape")
	if u, err := url.Parse(strings.TrimSpace(rawURL)); err == nil {
		if host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."); host != "" {
			act.Detail, act.Params["host"] = job.Ptr("from "+host), host
		}
	}
	return act
}
