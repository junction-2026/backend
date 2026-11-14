package adapter

import (
	"context"
	"encoding/json"
	"fmt"
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
	},
	"required":             []string{"topics", "news"},
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
	}
	if json.Unmarshal(raw, &in) != nil {
		return invalidTopics("topics, news and their members must be an array, objects and strings")
	}
	if in.Topics == nil || in.News == nil {
		return invalidTopics("topics and news must be arrays")
	}
	res := struct {
		Topics         []topic         `json:"topics"`
		News           []newsItem      `json:"news"`
		InputTruncated bool            `json:"input_truncated"`
		Usage          json.RawMessage `json:"usage"`
	}{[]topic{}, []newsItem{}, truncated, usage}
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
