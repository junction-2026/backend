package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// TopicExtractionSchema is the JSON schema of the topic_extraction reply.
var TopicExtractionSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"topics": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":       map[string]any{"type": "string"},
				"query":       map[string]any{"type": "string"},
				"description": map[string]any{"type": []string{"string", "null"}},
			},
			"required":             []string{"title", "query", "description"},
			"additionalProperties": false,
		}},
		"news": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"headline": map[string]any{"type": "string"},
				"summary":  map[string]any{"type": []string{"string", "null"}},
			},
			"required":             []string{"headline", "summary"},
			"additionalProperties": false,
		}},
		"claims": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":             map[string]any{"type": "string"},
				"language":         map[string]any{"type": "string"},
				"query":            map[string]any{"type": "string"},
				"query_en":         map[string]any{"type": "string"},
				"entities":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"check_worthiness": map[string]any{"type": "number"},
			},
			"required":             []string{"text", "language", "query", "query_en", "entities", "check_worthiness"},
			"additionalProperties": false,
		}},
	},
	"required":             []string{"topics", "news", "claims"},
	"additionalProperties": false,
}

type topic struct {
	Title       string  `json:"title"`
	Query       string  `json:"query"`
	Description *string `json:"description"`
}

type newsItem struct {
	Headline string  `json:"headline"`
	Summary  *string `json:"summary"`
}

type claim struct {
	Text            string   `json:"text"`
	Language        string   `json:"language"`
	Query           string   `json:"query"`
	QueryEN         string   `json:"query_en"`
	Entities        []string `json:"entities"`
	CheckWorthiness float64  `json:"check_worthiness"`
}

// topicExtraction asks the llm for the fact-checkable topics and news of the
// job's normalized_content (cut to max_input_chars) and returns the
// TopicExtraction object.
func (x *Executor) topicExtraction(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := def.Execution
	content, _ := req.Inputs["content"].(map[string]any)
	text, _ := content["normalized_content"].(string)
	if strings.TrimSpace(text) == "" {
		return nil, adapterErr("job input has no normalized_content to analyze")
	}
	runes := []rune(text)
	truncated := len(runes) > e.MaxChars()
	if truncated {
		text = cutAtSpace(runes, e.MaxChars())
	}
	raw, usage, err := LLM(ctx, def, topicUserMessage(def, text))
	if err != nil {
		return nil, err
	}
	var in struct {
		Topics *[]struct{ Title, Query, Description *string } `json:"topics"`
		News   *[]struct{ Headline, Summary *string }         `json:"news"`
		Claims *[]*struct {
			Text, Language, Query *string
			QueryEN               *string   `json:"query_en"`
			Entities              *[]string `json:"entities"`
			CheckWorthiness       *float64  `json:"check_worthiness"`
		} `json:"claims"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return invalidTopics("topics, news and their members must be an array, objects and strings")
	}
	if in.Topics == nil || in.News == nil || in.Claims == nil {
		return invalidTopics("topics, news and claims must be arrays")
	}
	res := struct {
		Topics         []topic         `json:"topics"`
		News           []newsItem      `json:"news"`
		Claims         []claim         `json:"claims"`
		InputTruncated bool            `json:"input_truncated"`
		Usage          json.RawMessage `json:"usage"`
	}{[]topic{}, []newsItem{}, []claim{}, truncated, usage}
	blank := func(s *string) bool { return s == nil || strings.TrimSpace(*s) == "" }
	for i, t := range *in.Topics {
		if blank(t.Title) || blank(t.Query) {
			return invalidTopics(fmt.Sprintf("topics[%d] needs a non-empty title and query", i))
		}
		res.Topics = append(res.Topics, topic{*t.Title, *t.Query, t.Description})
	}
	for i, n := range *in.News {
		if blank(n.Headline) {
			return invalidTopics(fmt.Sprintf("news[%d] needs a non-empty headline", i))
		}
		res.News = append(res.News, newsItem{*n.Headline, n.Summary})
	}
	for i, c := range *in.Claims {
		if c == nil || blank(c.Text) || blank(c.Language) || blank(c.Query) || blank(c.QueryEN) || c.CheckWorthiness == nil {
			return invalidTopics(fmt.Sprintf("claims[%d] needs non-empty text, language, query and query_en and a numeric check_worthiness", i))
		}
		ents := []string{}
		if c.Entities != nil {
			ents = *c.Entities
		}
		res.Claims = append(res.Claims, claim{*c.Text, *c.Language, *c.Query, *c.QueryEN, ents, min(max(*c.CheckWorthiness, 0), 1)})
	}
	sort.SliceStable(res.Claims, func(a, b int) bool { return res.Claims[a].CheckWorthiness > res.Claims[b].CheckWorthiness })
	if n := def.Execution.ExtractorMaxClaims(); len(res.Claims) > n {
		res.Claims = res.Claims[:n]
	}
	out, err := json.Marshal(res)
	if err != nil {
		return nil, adapterErr("topic extraction result is not serializable")
	}
	return out, nil
}

// topicUserMessage is the untrusted text inside <content> (its closing tag
// escaped), then the schema unless json_schema mode carries it.
func topicUserMessage(def *registry.Analyzer, text string) string {
	msg := "<content>\n" + closeContentRe.ReplaceAllString(text, `<\/$1>`) + "\n</content>"
	if def.Execution.ResponseFormat != registry.FormatJSONSchema {
		s, _ := json.Marshal(TopicExtractionSchema)
		msg += "\n<schema>\n" + string(s) + "\n</schema>"
	}
	return msg
}

func invalidTopics(msg string) (json.RawMessage, error) {
	return nil, &job.RunError{Code: job.CodeInvalidOutput, Message: "invalid topic extraction reply: " + msg}
}
