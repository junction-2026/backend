package job

import (
	"encoding/json"
	"regexp"
	"strings"
)

var langCode = regexp.MustCompile(`^[a-z]{2}$`)

// ReadSourceLanguage is the trimmed, lower-cased source_language of a translation result when it is a two-letter
// code, "" when unknown.
func ReadSourceLanguage(result any) string {
	b, _ := json.Marshal(result)
	var tr struct {
		SourceLanguage any `json:"source_language"`
	}
	_ = json.Unmarshal(b, &tr)
	s, _ := tr.SourceLanguage.(string)
	if s = strings.ToLower(strings.TrimSpace(s)); langCode.MatchString(s) {
		return s
	}
	return ""
}

// SourceLanguage is the job language ($job.source_language): the normalized source_language of the job's succeeded
// translation run, "" when unknown.
func (j *Job) SourceLanguage() string {
	for i := range j.Analyzers {
		if r := &j.Analyzers[i]; r.ID == "translation" && r.Status == RunSucceeded {
			return ReadSourceLanguage(r.Result)
		}
	}
	return ""
}
