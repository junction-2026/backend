package socialscraper

import (
	"bytes"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxURLChars  = 2048
	maxBodyBytes = 64 << 10
)

// parseBody validates the request body. Members are decoded as json.RawMessage so each can be checked
// for its JSON type; every offending member is reported. Unknown members are ignored.
func parseBody(body []byte) (rawURL string, req Request, err *Error) {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return "", req, InvalidRequest("the request body is not valid JSON")
	}
	if m == nil {
		return "", req, InvalidRequest("the request body must be a JSON object")
	}
	fields := map[string]string{}
	rawURL = checkURL(m["url"], m, fields)
	req.MaxPosts = checkInt(m, "max_posts", 20, 1, 100, fields)
	req.MaxComments = checkInt(m, "max_comments", 50, 0, 200, fields)
	if len(fields) > 0 {
		return "", req, ValidationFailed(fields)
	}
	return rawURL, req, nil
}

func checkURL(raw json.RawMessage, m map[string]json.RawMessage, fields map[string]string) string {
	if _, present := m["url"]; !present {
		fields["url"] = "is required"
		return ""
	}
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		fields["url"] = "must be a string"
		return ""
	}
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxURLChars {
		fields["url"] = "must be at most 2048 characters"
		return ""
	}
	if u, err := url.Parse(s); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		fields["url"] = "must be an absolute http or https URL"
		return ""
	}
	return s
}

func checkInt(m map[string]json.RawMessage, name string, def, lo, hi int, fields map[string]string) int {
	raw, present := m[name]
	if !present {
		return def
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) { // only a JSON number starts so
		fields[name] = "must be an integer"
		return def
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || f != math.Trunc(f) {
		fields[name] = "must be an integer"
		return def
	}
	if f < float64(lo) || f > float64(hi) {
		fields[name] = "must be between " + strconv.Itoa(lo) + " and " + strconv.Itoa(hi)
		return def
	}
	return int(f)
}
