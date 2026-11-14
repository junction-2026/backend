package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/junction-2026/backend/internal/chat"
	"github.com/junction-2026/backend/internal/job"
)

const (
	maxChatMessages = 40
	maxUserChars    = 2000
	maxAssistChars  = 8000
)

type chatReply struct {
	JobID            string       `json:"job_id"`
	Message          chat.Message `json:"message"`
	HistoryTruncated bool         `json:"history_truncated"`
	CreatedAt        time.Time    `json:"created_at"`
}

// parseChat validates a ChatRequest (shared "ChatRequest validation" and "ChatMessage content limits").
func parseChat(body json.RawMessage) (msgs []chat.Message, language string, f fields) {
	f = fields{}
	top, ok := object(body)
	if !ok {
		f["body"] = "must be a JSON object"
		return
	}
	language, _ = f.str(top, "language", "language")
	raw, present := member(top, "messages")
	var elems []json.RawMessage
	switch {
	case !present:
		f["messages"] = "is required"
		return
	case json.Unmarshal(raw, &elems) != nil:
		f["messages"] = "must be an array"
		return
	case len(elems) == 0 || len(elems) > maxChatMessages:
		f["messages"] = fmt.Sprintf("must have 1 to %d elements", maxChatMessages)
		return
	}
	for i, el := range elems {
		path := fmt.Sprintf("messages[%d]", i)
		m, ok := object(el)
		if !ok {
			f[path] = "must be an object"
			continue
		}
		role, _ := f.str(m, "role", path+".role")
		if role != "user" && role != "assistant" {
			if f[path+".role"] == nil {
				f[path+".role"] = "must be user or assistant"
			}
		}
		content, isStr := f.str(m, "content", path+".content")
		limit := maxUserChars
		if role == "assistant" {
			limit = maxAssistChars
		}
		switch {
		case !isStr && f[path+".content"] == nil:
			f[path+".content"] = "is required"
		case isStr && strings.TrimSpace(content) == "":
			f[path+".content"] = "must not be blank"
		case isStr && utf8.RuneCountInString(content) > limit:
			f[path+".content"] = fmt.Sprintf("must have at most %d characters", limit)
		}
		msgs = append(msgs, chat.Message{Role: role, Content: content})
	}
	if len(msgs) > 0 && msgs[len(msgs)-1].Role != "user" {
		f["messages"] = "the last message must have role user"
	}
	return
}

// postChat serves the shared analysis-chat-api endpoint. It only reads the job.
func (a *api) postChat(w http.ResponseWriter, r *http.Request) {
	raw, ok := readJSON(w, r)
	if !ok {
		return
	}
	msgs, language, f := parseChat(raw)
	if len(f) > 0 {
		WriteError(w, http.StatusUnprocessableEntity, "validation_failed", "request body violates the schema", map[string]any{"fields": f})
		return
	}
	j, err := a.svc.Get(r.PathValue("id"))
	if err != nil {
		a.jobError(w, err)
		return
	}
	if j.Status != job.StatusCompleted {
		WriteError(w, http.StatusConflict, "job_not_completed", "job is not completed", map[string]any{"status": j.Status})
		return
	}
	if !a.chat.Enabled() {
		WriteError(w, http.StatusServiceUnavailable, "chat_unavailable", "chat is not available", nil)
		return
	}
	release, retry := a.chat.Acquire(j.ID)
	if release == nil {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many chat requests for this job", map[string]any{"retry_after_s": retry})
		return
	}
	defer release()
	window, truncated := chat.Window(msgs)
	text, err := a.chat.Reply(r.Context(), j, window, language)
	switch {
	case errors.Is(err, chat.ErrTimeout):
		WriteError(w, http.StatusGatewayTimeout, "chat_timeout", "the assistant did not answer in time", nil)
	case err != nil:
		WriteError(w, http.StatusServiceUnavailable, "chat_unavailable", "chat is not available", nil)
	default:
		writeJSON(w, http.StatusOK, chatReply{JobID: j.ID, Message: chat.Message{Role: "assistant", Content: text},
			HistoryTruncated: truncated, CreatedAt: time.Now().UTC()})
	}
}
