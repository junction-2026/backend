package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
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
		"wikipedia_quotes": map[string]any{"type": "array", "items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"claim": map[string]any{"type": "string"}, "language": map[string]any{"type": "string"}, "quote": map[string]any{"type": "string"},
			},
			"required":             []string{"claim", "language", "quote"},
			"additionalProperties": false,
		}},
	},
	"required":             []string{"verdict", "confidence", "user_text", "wikipedia_quotes"},
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

// LLMReply is a successful llm call: the decoded JSON object, the provider's
// usage (both attempts combined after a reply retry; nil when none reported),
// the number of requests whose replies were judged and the retry reason (nil
// when no reply retry was sent).
type LLMReply struct {
	Out         json.RawMessage
	Usage       json.RawMessage
	Attempts    int
	RetryReason *string
}

// Reply retry reasons.
const (
	RetryLength      = "length"
	RetryInvalidJSON = "invalid_json"
	RetrySchema      = "schema"
)

const retryLengthText = "Your previous reply was cut off. Reply again with only the JSON object, and keep user_text within the word limit."

// LLM calls the OpenAI-compatible {base_url}/chat/completions endpoint of the
// llm def (finalizer, topic_extraction or technique spans). check (nil: none)
// applies the caller's schema and built-in checks to the decoded object. A
// reply that is cut off, not a JSON object or fails check is retried once
// (see the registry's retry_* settings). Errors are *job.RunError and never
// contain the API key, the URL or reply content.
func LLM(ctx context.Context, def *registry.Analyzer, user string, check func(json.RawMessage) error) (LLMReply, error) {
	e := &def.Execution
	msgs := []map[string]string{
		{"role": "system", "content": e.Prompt},
		{"role": "user", "content": user},
	}
	body, err := llmCall(ctx, e, e.Model, e.MaxTokens, msgs)
	if err != nil {
		return LLMReply{}, err
	}
	out, usage, reason, err := judgeReply(body, check)
	if err == nil {
		return LLMReply{Out: out, Usage: usage, Attempts: 1}, nil
	}
	if reason == "" || !e.RetryEnabled() || !timeLeft(ctx, e.RetryMinRemainingD()) {
		return LLMReply{}, err
	}
	first := err.(*job.RunError).Message
	model, maxTokens := e.Model, e.MaxTokens
	if e.RetryModel != "" {
		model = e.RetryModel
	}
	text := "Your previous reply was not a valid JSON object matching the required schema (" + retryHint(first) + "). Reply again with only that JSON object."
	if reason == RetryLength {
		text, maxTokens = retryLengthText, e.RetryMaxTokensN()
	}
	slog.Info("llm reply retry", "analyzer_id", def.ID, "reason", reason, "model", model)
	body, err = llmCall(ctx, e, model, maxTokens, append(msgs, map[string]string{"role": "user", "content": text}))
	if err != nil {
		return LLMReply{}, err
	}
	out, usage2, _, err := judgeReply(body, check)
	if err != nil {
		return LLMReply{}, &job.RunError{Code: job.CodeInvalidOutput, Message: "first: " + first + "; retry: " + err.(*job.RunError).Message}
	}
	return LLMReply{Out: out, Usage: mergeUsage(usage, usage2), Attempts: 2, RetryReason: &reason}, nil
}

// timeLeft reports whether at least need remains before ctx's deadline.
func timeLeft(ctx context.Context, need time.Duration) bool {
	dl, ok := ctx.Deadline()
	return !ok || time.Until(dl) >= need
}

// retryHint is the reason quoted in the retry message: at most 120 chars and
// cut before the first ": ", after which a schema error may quote reply values.
func retryHint(msg string) string {
	msg, _, _ = strings.Cut(msg, ": ")
	if r := []rune(msg); len(r) > 120 {
		msg = string(r[:120])
	}
	return msg
}

// judgeReply decodes body and applies check; reason is the retry reason of an
// invalid reply ("" when it must not be retried). usage is returned even then.
func judgeReply(body []byte, check func(json.RawMessage) error) (out, usage json.RawMessage, reason string, err error) {
	out, usage, reason, err = decodeReply(body)
	if err == nil && check != nil {
		if cerr := check(out); cerr != nil {
			return nil, usage, RetrySchema, &job.RunError{Code: job.CodeInvalidOutput, Message: cerr.Error()}
		}
	}
	return out, usage, reason, err
}

// llmCall sends one chat completion request (retried once on a connection
// error, 429 or 5xx) and returns the body of the 2xx answer.
func llmCall(ctx context.Context, e *registry.Execution, model string, maxTokens int, msgs []map[string]string) ([]byte, error) {
	req := map[string]any{
		"model":       model,
		"temperature": e.Temperature,
		"max_tokens":  maxTokens,
		"messages":    msgs,
	}
	schema, name := any(FinalizationSchema), "finalization"
	switch {
	case e.TopicExtraction():
		schema, name = TopicExtractionSchema, "topic_extraction"
	case e.Adapter == registry.AdapterTechniqueClassifier: // the spans.llm Execution
		schema, name = TechniqueSpanSchema, "technique_spans"
	case e.Adapter == registry.AdapterLLMTechniques:
		schema, name = LLMTechniquesSchema, "llm_techniques"
	}
	switch e.ResponseFormat {
	case registry.FormatJSONSchema:
		req["response_format"] = map[string]any{"type": "json_schema",
			"json_schema": map[string]any{"name": name, "strict": true, "schema": schema}}
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
		return nil, adapterErr("llm request is not serializable")
	}
	url := strings.TrimRight(e.BaseURL, "/") + "/chat/completions"

	status, body, retry, wait, err := llmSend(ctx, e, url, buf.Bytes())
	if retry {
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ContextError(ctx)
		case <-t.C:
		}
		status, body, _, _, err = llmSend(ctx, e, url, buf.Bytes())
	}
	if err != nil {
		return nil, err
	}
	if status < 200 || status > 299 {
		return nil, adapterErr("%s", providerError(status, body, secrets(e)))
	}
	return body, nil
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

// secrets are the configured values a provider might echo: the API key and
// every header value. Values under 4 bytes are skipped: redacting them would
// garble the message without hiding a real secret.
func secrets(e *registry.Execution) []string {
	out := []string{e.APIKey}
	for _, v := range e.Headers {
		out = append(out, v)
	}
	return out
}

// providerError is the redacted adapter_error message of a non-2xx answer.
func providerError(status int, body []byte, secrets []string) string {
	var p struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &p)
	msg := "llm provider HTTP " + strconv.Itoa(status)
	if m := strings.TrimSpace(p.Error.Message); m != "" {
		for _, s := range secrets { // before truncating, so no secret prefix survives
			if len(s) >= 4 {
				m = strings.ReplaceAll(m, s, "[REDACTED]")
			}
		}
		msg += ": " + truncate(m, 300)
	}
	return msg
}

func invalidOutput(usage json.RawMessage, reason, msg string) (json.RawMessage, json.RawMessage, string, error) {
	return nil, usage, reason, &job.RunError{Code: job.CodeInvalidOutput, Message: msg}
}

// decodeReply extracts the JSON object from choices[0].message.content. An
// invalid reply returns its retry reason ("" for a refusal, content_filter or
// another finish_reason, which are not retried) and the usage reported.
func decodeReply(body []byte) (out, usage json.RawMessage, reason string, err error) {
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
		return invalidOutput(nil, "", "llm reply has no choices")
	}
	if bytes.HasPrefix(r.Usage, []byte("{")) {
		usage = r.Usage
	}
	c := r.Choices[0]
	switch {
	case c.Message.Refusal != nil && strings.TrimSpace(*c.Message.Refusal) != "":
		return invalidOutput(usage, "", "llm refused to answer")
	case c.FinishReason == "length":
		return invalidOutput(usage, RetryLength, `llm reply finish_reason is "length", not "stop"`)
	case c.FinishReason != "stop":
		return invalidOutput(usage, "", "llm reply finish_reason is "+strconv.Quote(truncate(c.FinishReason, 40))+", not \"stop\"")
	case c.Message.Content == nil || strings.TrimSpace(*c.Message.Content) == "":
		return invalidOutput(usage, RetryInvalidJSON, "llm reply content is empty")
	}
	out = []byte(unfence(*c.Message.Content))
	if !json.Valid(out) || out[0] != '{' {
		return invalidOutput(usage, RetryInvalidJSON, "llm reply content is not a JSON object")
	}
	return out, usage, "", nil
}

// mergeUsage combines two provider usage objects: numbers are summed, objects
// merged recursively, a member of one report is kept, any other member takes
// b's value. nil only when both are nil.
func mergeUsage(a, b json.RawMessage) json.RawMessage {
	if a == nil || b == nil {
		if a == nil {
			return b
		}
		return a
	}
	var x, y any
	if decodeNumbers(a, &x) != nil || decodeNumbers(b, &y) != nil {
		return b
	}
	out, err := json.Marshal(mergeValue(x, y))
	if err != nil {
		return b
	}
	return out
}

func decodeNumbers(raw json.RawMessage, v *any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}

func mergeValue(a, b any) any {
	switch x := a.(type) {
	case json.Number:
		if y, ok := b.(json.Number); ok {
			return addNumbers(x, y)
		}
	case map[string]any:
		if y, ok := b.(map[string]any); ok {
			for k, v := range y {
				if old, ok := x[k]; ok {
					x[k] = mergeValue(old, v)
				} else {
					x[k] = v
				}
			}
			return x
		}
	}
	if b == nil {
		return a
	}
	return b
}

// addNumbers sums exactly (0.0012 + 0.0010 is 0.0022, not 0.0021999…).
func addNumbers(a, b json.Number) json.Number {
	x, ok1 := new(big.Rat).SetString(string(a))
	y, ok2 := new(big.Rat).SetString(string(b))
	if !ok1 || !ok2 {
		return b
	}
	x.Add(x, y)
	if x.IsInt() {
		return json.Number(x.Num().String())
	}
	f, _ := x.Float64()
	return json.Number(strconv.FormatFloat(f, 'g', -1, 64))
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
