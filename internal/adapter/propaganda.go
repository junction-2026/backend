package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/junction-2026/backend/internal/registry"
)

// propaganda calls an IDA-SERICS/PropagandaDetection service: one POST of
// {"text": normalized_content}; a validated response object is the result.
func (x *Executor) propaganda(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	text, ok := propagandaText(def, req.Inputs)
	if !ok {
		return nil, adapterErr("no text to analyze")
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, adapterErr("analyzer inputs are not serializable")
	}
	resp, err := x.send(ctx, def, req, http.MethodPost, def.Execution.Endpoint, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, adapterErr("analyzer service returned HTTP %d", resp.StatusCode)
	}
	raw, err := readObject(ctx, resp)
	if err != nil {
		return nil, err
	}
	if msg := checkPropaganda(raw); msg != "" {
		return nil, adapterErr("invalid propaganda detection response: %s", msg)
	}
	return raw, nil
}

// propagandaText is the job's normalized_content (default or text: $input) or,
// for text: $analyzers.<id>, that dependency's translated_text. The latter
// never falls back to normalized_content.
func propagandaText(def *registry.Analyzer, in map[string]any) (string, bool) {
	if !strings.HasPrefix(def.Input.Bindings["text"], "$analyzers.") {
		key := "content"
		if def.Input.Bindings["text"] == "$input" {
			key = "text"
		}
		input, _ := in[key].(map[string]any)
		s, ok := input["normalized_content"].(string)
		return s, ok
	}
	b, err := json.Marshal(in["text"])
	var r struct {
		T *string `json:"translated_text"`
	}
	if err != nil || json.Unmarshal(b, &r) != nil || r.T == nil {
		return "", false
	}
	return *r.T, true
}

// checkPropaganda returns what is wrong with raw (a JSON object), or "".
func checkPropaganda(raw json.RawMessage) string {
	var o struct {
		Result    json.RawMessage `json:"result"`
		LatencyMS json.RawMessage `json:"latency_ms"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return "unreadable object"
	}
	var items []json.RawMessage
	if json.Unmarshal(o.Result, &items) != nil || len(items) == 0 {
		return "result must be a non-empty array"
	}
	for _, it := range items {
		var e struct {
			Label *string  `json:"label"`
			Score *float64 `json:"score"`
		}
		if json.Unmarshal(it, &e) != nil || !bytes.HasPrefix(it, []byte("{")) {
			return "result elements must be objects with a label and a numeric score"
		}
		if e.Label == nil || (*e.Label != "PROPAGANDA" && *e.Label != "NO_PROPAGANDA") {
			return "label must be PROPAGANDA or NO_PROPAGANDA"
		}
		if e.Score == nil || *e.Score < 0 || *e.Score > 1 {
			return "score must be a number in [0, 1]"
		}
	}
	if o.LatencyMS != nil {
		var l *float64 // null decodes to nil and is rejected like any non-number
		if json.Unmarshal(o.LatencyMS, &l) != nil || l == nil || *l < 0 {
			return "latency_ms must be a non-negative number"
		}
	}
	return ""
}
