package instagram

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
)

// session is the content of INSTAGRAM_SESSION_FILE.
type session struct {
	SessionID string `json:"sessionid"`
	CSRFToken string `json:"csrftoken"`
	DSUserID  string `json:"ds_user_id"`
}

func (s *session) cookie() string {
	parts := []string{"sessionid=" + s.SessionID}
	if s.CSRFToken != "" {
		parts = append(parts, "csrftoken="+s.CSRFToken)
	}
	if s.DSUserID != "" {
		parts = append(parts, "ds_user_id="+s.DSUserID)
	}
	return strings.Join(parts, "; ")
}

// cookieSafe reports a value that can sit in a Cookie header unchanged.
func cookieSafe(v string) bool {
	for _, c := range v {
		if c <= ' ' || c >= 0x7f || c == ';' || c == ',' || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}

// loadSession reads the session file; nil (anonymous) when path is empty, or the file is missing, unreadable,
// not the expected JSON or lacks a usable sessionid. The one log line carries neither path nor error text.
func loadSession(path string, log *slog.Logger) *session {
	if path == "" {
		return nil
	}
	var s session
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	if err != nil || s.SessionID == "" || !cookieSafe(s.SessionID) || !cookieSafe(s.CSRFToken) || !cookieSafe(s.DSUserID) {
		if log != nil {
			log.Warn("instagram session file could not be loaded; scraping anonymously")
		}
		return nil
	}
	return &s
}
