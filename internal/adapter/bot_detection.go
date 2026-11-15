package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

// Labels of the multilingual bot detector (shared change bot-detection-text-result).
const (
	botModelHuman = "LABEL_0"
	botModelBot   = "LABEL_1"
)

type botPrediction struct {
	Label string
	Score float64
}

// botDetection classifies the job's original normalized_content (never a translation) with the bot-detector service:
// one POST of {"text": ...}; the answer is mapped to {label, score, scope: "text", model_label, model_score}.
func (x *Executor) botDetection(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	input, _ := req.Inputs["content"].(map[string]any)
	text, _ := input["normalized_content"].(string)
	if strings.TrimSpace(text) == "" {
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, adapterErr("analyzer service returned HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		if ce := ContextError(ctx); ce != nil {
			return nil, ce
		}
		return nil, adapterErr("reading analyzer response failed")
	}
	preds, msg := parseBotResponse(b)
	if msg == "" {
		var out json.RawMessage
		if out, msg = mapBotLabels(preds); msg == "" {
			return out, nil
		}
	}
	return nil, &job.RunError{Code: job.CodeInvalidOutput,
		Message: redactor(secrets(&def.Execution)...)("invalid bot detection response: " + msg)}
}

// parseBotResponse reads {label, score}, [{...}], [[{...}]] or {"result": <any of these>} into its predictions.
// The message it returns never repeats response content.
func parseBotResponse(b []byte) ([]botPrediction, string) {
	var v any
	if json.Unmarshal(bytes.TrimSpace(b), &v) != nil {
		return nil, "body is not JSON"
	}
	if o, ok := v.(map[string]any); ok && o["result"] != nil {
		v = o["result"]
	}
	var items []any
	if arr, ok := v.([]any); ok {
		for _, it := range arr {
			if inner, ok := it.([]any); ok {
				items = append(items, inner...)
			} else {
				items = append(items, it)
			}
		}
	} else {
		items = []any{v}
	}
	var out []botPrediction
	for _, it := range items {
		o, _ := it.(map[string]any)
		label, _ := o["label"].(string)
		score, isNum := o["score"].(float64)
		switch {
		case o == nil:
			return nil, "answer must be an object or a list of objects with a label and a score"
		case label == "":
			return nil, "a label is missing"
		case !isNum || !inUnit(score):
			return nil, "a score is not a number in [0, 1]"
		}
		out = append(out, botPrediction{label, score})
	}
	if len(out) == 0 {
		return nil, "answer has no label"
	}
	return out, ""
}

// mapBotLabels picks the top prediction and builds the TextBotDetection result. score is always P(bot_or_generated).
func mapBotLabels(preds []botPrediction) (json.RawMessage, string) {
	top := preds[0]
	var bot *float64
	seen := map[string]bool{}
	for i, p := range preds {
		switch p.Label {
		case botModelBot:
			bot = &preds[i].Score
		case botModelHuman:
		default:
			return nil, "unknown label"
		}
		if seen[p.Label] {
			return nil, "a label is duplicated"
		}
		seen[p.Label] = true
		if p.Score > top.Score {
			top = p
		}
	}
	label, score := "human", 0.0
	if top.Label == botModelBot {
		label = "bot_or_generated"
	}
	switch {
	case bot != nil:
		score = *bot
	case top.Label == botModelHuman:
		score = math.Round((1-top.Score)*1e10) / 1e10 // hides float noise: 1-0.9 -> 0.1
	}
	out, _ := json.Marshal(map[string]any{"label": label, "score": score, "scope": "text",
		"model_label": top.Label, "model_score": top.Score})
	return out, ""
}
