package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const (
	llmBackoff       = time.Second
	llmMaxRetryAfter = 5 * time.Second
)

// FinalizationSchema is the JSON schema of the llm finalizer's reply.
var FinalizationSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"verdict":    map[string]any{"type": "string", "enum": []string{"detected", "not_detected", "uncertain"}},
		"confidence": map[string]any{"type": []string{"number", "null"}, "minimum": 0, "maximum": 1},
		"user_text":  map[string]any{"type": "string", "minLength": 1},
	},
	"required":             []string{"verdict", "confidence", "user_text"},
	"additionalProperties": false,
}

var closeContentRe = regexp.MustCompile(`(?i)</(content)>`)

// LLMUserMessage assembles the user message: the untrusted content inside
// <content> (its closing tag escaped so it cannot end the block), then one
// compact JSON entry per analyzer, then the schema unless json_schema mode
// carries it in response_format.
func LLMUserMessage(def *registry.Analyzer, content string, entries []json.RawMessage) string {
	var b strings.Builder
	b.WriteString("<content>\n" + closeContentRe.ReplaceAllString(content, `<\/$1>`) + "\n</content>\n<analyzers>\n")
	for _, e := range entries {
		b.Write(e)
		b.WriteByte('\n')
	}
	b.WriteString("</analyzers>")
	if def.Execution.ResponseFormat != registry.FormatJSONSchema {
		s, _ := json.Marshal(FinalizationSchema)
		b.WriteString("\n<schema>\n" + string(s) + "\n</schema>")
	}
	return b.String()
}

// LLM calls the OpenAI-compatible {base_url}/chat/completions endpoint of the
// llm finalizer def and returns the JSON object decoded from the model's reply
// plus the provider's usage object (nil when absent). Errors are *job.RunError
// and never contain the API key or the URL.
func LLM(ctx context.Context, def *registry.Analyzer, user string) (out, usage json.RawMessage, err error) {
	e := &def.Execution
	req := map[string]any{
		"model":       e.Model,
		"temperature": e.Temperature,
		"max_tokens":  e.MaxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": e.Prompt},
			{"role": "user", "content": user},
		},
	}
	switch e.ResponseFormat {
	case registry.FormatJSONSchema:
		req["response_format"] = map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": "finalization", "strict": true, "schema": FinalizationSchema}}
	case registry.FormatJSONObject:
		req["response_format"] = map[string]any{"type": "json_object"}
	}
	for k, v := range e.ExtraBody { // the registry rejects model and messages
		req[k] = v
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return nil, nil, adapterErr("llm request is not serializable")
	}
	url := strings.TrimRight(e.BaseURL, "/") + "/chat/completions"

	status, body, retry, wait, err := llmSend(ctx, e, url, buf.Bytes())
	if retry {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, nil, ContextError(ctx)
		case <-t.C:
		}
		status, body, _, _, err = llmSend(ctx, e, url, buf.Bytes())
	}
	if err != nil {
		return nil, nil, err
	}
	if status < 200 || status > 299 {
		return nil, nil, adapterErr("%s", providerError(status, body, e.APIKey))
	}
	return decodeReply(body)
}

// llmSend performs one POST and reads the whole answer. retry is set for a
// connection error before any response, 429 and 5xx; wait is the backoff
// before that retry (a 429's Retry-After seconds, capped).
func llmSend(ctx context.Context, e *registry.Execution, url string, body []byte) (status int, b []byte, retry bool, wait time.Duration, err error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, false, 0, adapterErr("invalid llm request")
	}
	for k, v := range e.Headers {
		hr.Header.Set(k, v)
	}
	hr.Header.Set("Content-Type", "application/json") // set last: configured headers cannot override these
	hr.Header.Set("Authorization", "Bearer "+e.APIKey)
	resp, err := noRedirects.Do(hr)
	if err != nil {
		if e := ContextError(ctx); e != nil {
			return 0, nil, false, 0, e
		}
		return 0, nil, true, llmBackoff, adapterErr("llm provider request failed")
	}
	defer resp.Body.Close()
	b, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if e := ContextError(ctx); e != nil {
			return 0, nil, false, 0, e
		}
		return 0, nil, false, 0, adapterErr("reading llm provider response failed")
	}
	wait = llmBackoff
	if s, perr := strconv.Atoi(resp.Header.Get("Retry-After")); resp.StatusCode == http.StatusTooManyRequests && perr == nil && s >= 0 {
		wait = min(time.Duration(s)*time.Second, llmMaxRetryAfter)
	}
	retry = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
	return resp.StatusCode, b, retry, wait, nil
}

// providerError is the redacted adapter_error message of a non-2xx answer.
func providerError(status int, body []byte, key string) string {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &p)
	msg := "llm provider HTTP " + strconv.Itoa(status)
	if m := strings.TrimSpace(p.Error.Message); m != "" {
		if key != "" {
			m = strings.ReplaceAll(m, key, "[REDACTED]") // before truncating, so no key prefix survives
		}
		msg += ": " + truncate(m, 300)
	}
	return msg
}

func invalidOutput(msg string) (json.RawMessage, json.RawMessage, error) {
	return nil, nil, &job.RunError{Code: job.CodeInvalidOutput, Message: msg}
}

// decodeReply extracts the JSON object from choices[0].message.content.
func decodeReply(body []byte) (json.RawMessage, json.RawMessage, error) {
	var r struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content *string `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(body, &r) != nil || len(r.Choices) == 0 {
		return invalidOutput("llm reply has no choices")
	}
	c := r.Choices[0]
	switch {
	case c.FinishReason != "stop":
		return invalidOutput("llm reply finish_reason is " + strconv.Quote(truncate(c.FinishReason, 40)) + ", not \"stop\"")
	case c.Message.Refusal != nil && strings.TrimSpace(*c.Message.Refusal) != "":
		return invalidOutput("llm refused to answer")
	case c.Message.Content == nil || strings.TrimSpace(*c.Message.Content) == "":
		return invalidOutput("llm reply content is empty")
	}
	out := []byte(unfence(*c.Message.Content))
	if !json.Valid(out) || out[0] != '{' {
		return invalidOutput("llm reply content is not a JSON object")
	}
	var usage json.RawMessage
	if bytes.HasPrefix(r.Usage, []byte("{")) {
		usage = r.Usage
	}
	return out, usage, nil
}

// unfence removes one surrounding ``` or ```json code fence.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") || len(s) < 6 {
		return s
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "```"), "```")
	s = strings.TrimPrefix(s, "json")
	return strings.TrimSpace(s)
}
