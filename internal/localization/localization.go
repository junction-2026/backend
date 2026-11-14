// Package localization serves the shared job-text-localization texts: the finalizer user_text and the
// llm_techniques finding explanations of a done job in a UI language. It only reads the job; it never translates
// source material (input, spans, claims, articles, reviews, passages).
package localization

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/junction-2026/backend/internal/finalizer"
	"github.com/junction-2026/backend/internal/job"
)

// Supported are the localization languages (shared "Localization languages").
var Supported = []string{"en", "fr", "ar", "de", "fi", "ru"}

// Language returns the lower-case primary subtag of code and whether it is supported ("DE-at" -> "de").
func Language(code string) (string, bool) {
	p, _, _ := strings.Cut(strings.TrimSpace(code), "-")
	p = strings.ToLower(p)
	return p, slices.Contains(Supported, p)
}

// insufficient translates finalizer.InsufficientText (produced_by orchestrator) into every supported language.
var insufficient = map[string]string{
	"en": finalizer.InsufficientText,
	"fr": "Aucune analyse n'a produit de résultat exploitable, aucun verdict ne peut donc être rendu.",
	"ar": "لم يُسفر أيٌّ من التحليلات عن نتيجة قابلة للاستخدام، لذا يتعذّر إصدار حكم.",
	"de": "Keine Analyse hat ein verwertbares Ergebnis geliefert, daher kann kein Urteil abgegeben werden.",
	"fi": "Mikään analyysi ei tuottanut käyttökelpoista tulosta, joten arviota ei voida antaa.",
	"ru": "Ни один анализ не дал пригодного результата, поэтому вынести вердикт невозможно.",
}

// LocalizedText statuses.
const (
	StatusOriginal    = "original"
	StatusTranslated  = "translated"
	StatusUnavailable = "unavailable"
)

type LocalizedText struct {
	Status         string  `json:"status"`
	Text           *string `json:"text"`
	SourceLanguage *string `json:"source_language"`
}

type FinalAssessment struct {
	UserText LocalizedText `json:"user_text"`
}

type LLMFinding struct {
	Index       int           `json:"index"`
	Label       string        `json:"label"`
	Explanation LocalizedText `json:"explanation"`
}

type JobLocalization struct {
	JobID           string           `json:"job_id"`
	Language        string           `json:"language"`
	FinalAssessment *FinalAssessment `json:"final_assessment"`
	LLMFindings     []LLMFinding     `json:"llm_findings"`
}

// Translator translates texts into lang. The reply is validated by the Service (same count, non-empty entries).
// Error messages must not carry URLs, credentials, texts or provider messages: they are logged.
type Translator interface {
	Translate(ctx context.Context, lang string, texts []string) ([]string, error)
}

type key struct{ job, lang string }

type entry struct {
	k     key
	texts map[int]string // slot (position among the job's texts needing a translation) -> translation; never mutated
}

// Service localizes jobs. Its cache holds translated texts only, in memory.
type Service struct {
	tr      Translator // nil: no provider, texts needing a translation are unavailable
	timeout time.Duration
	log     *slog.Logger
	max     int // cache entries

	mu     sync.Mutex
	lru    *list.List // of *entry, most recently used first
	items  map[key]*list.Element
	flight map[key]chan struct{} // in-flight provider call per key
}

const (
	DefaultTimeout    = 20 * time.Second
	defaultMaxEntries = 1000
)

// New returns a Service; tr nil means no provider, timeout <= 0 the default, log nil slog.Default().
func New(tr Translator, timeout time.Duration, log *slog.Logger) *Service {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{tr: tr, timeout: timeout, log: log, max: defaultMaxEntries,
		lru: list.New(), items: map[key]*list.Element{}, flight: map[key]chan struct{}{}}
}

// Localize returns the localization of the done job j into the supported language lang. It reads j only.
func (s *Service) Localize(j job.Job, lang string) JobLocalization {
	loc := JobLocalization{JobID: j.ID, Language: lang, LLMFindings: []LLMFinding{}}
	var dst []*LocalizedText // texts needing a translation, in slot order
	var src []string
	need := func(t *LocalizedText, text string, from *string) {
		t.SourceLanguage = from
		if from != nil && strings.EqualFold(*from, lang) {
			t.Status, t.Text = StatusOriginal, &text
			return
		}
		t.Status = StatusUnavailable
		dst, src = append(dst, t), append(src, text)
	}

	var fa struct {
		UserText   string `json:"user_text"`
		ProducedBy string `json:"produced_by"`
	}
	if set(j.Finalization.Result) && json.Unmarshal(j.Finalization.Result, &fa) == nil {
		loc.FinalAssessment = &FinalAssessment{}
		t := &loc.FinalAssessment.UserText
		if fa.ProducedBy == "orchestrator" {
			text, status := insufficient[lang], StatusTranslated
			if lang == "en" {
				text, status = fa.UserText, StatusOriginal
			}
			*t = LocalizedText{Status: status, Text: &text, SourceLanguage: job.Ptr("en")}
		} else {
			need(t, fa.UserText, translationSource(j))
		}
	}

	var lt struct {
		ExplanationLanguage string `json:"explanation_language"`
		Findings            []struct {
			Label       string `json:"label"`
			Explanation string `json:"explanation"`
		} `json:"findings"`
	}
	if run := succeeded(j, "llm_techniques"); run != nil && json.Unmarshal(run.Result, &lt) == nil {
		loc.LLMFindings = make([]LLMFinding, len(lt.Findings))
		for i, f := range lt.Findings {
			loc.LLMFindings[i] = LLMFinding{Index: i, Label: f.Label}
			var from *string
			if lt.ExplanationLanguage != "" {
				from = &lt.ExplanationLanguage
			}
			need(&loc.LLMFindings[i].Explanation, f.Explanation, from)
		}
	}

	if len(src) > 0 {
		for i, text := range s.translated(key{j.ID, lang}, lang, src) {
			dst[i].Status, dst[i].Text = StatusTranslated, &text
		}
	}
	return loc
}

// translated returns the translations of texts by slot, from the cache or one provider call shared by concurrent
// identical requests. Missing slots are unavailable and are not cached.
func (s *Service) translated(k key, lang string, texts []string) map[int]string {
	s.mu.Lock()
	if wait, busy := s.flight[k]; busy {
		s.mu.Unlock()
		<-wait // bounded by the leader's timeout; its translations are in the cache now
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.cached(k)
	}
	got := s.cached(k)
	var miss []int
	for i := range texts {
		if _, ok := got[i]; !ok {
			miss = append(miss, i)
		}
	}
	if len(miss) == 0 {
		s.mu.Unlock()
		return got
	}
	done := make(chan struct{})
	s.flight[k] = done
	s.mu.Unlock()

	var out []string
	defer func() { // also on a provider panic, so waiters are never stuck
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, slot := range miss {
			if i < len(out) && strings.TrimSpace(out[i]) != "" {
				got[slot] = out[i]
			}
		}
		if len(got) > 0 {
			s.store(k, got)
		}
		delete(s.flight, k)
		close(done)
	}()
	in := make([]string, len(miss))
	for i, slot := range miss {
		in[i] = texts[slot]
	}
	out = s.call(k, lang, in)
	return got
}

// call sends one provider request; nil when there is no provider, it failed or the reply count is wrong.
func (s *Service) call(k key, lang string, in []string) []string {
	if s.tr == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout) // not the request's: waiters share the call
	defer cancel()
	out, err := s.tr.Translate(ctx, lang, in)
	switch {
	case ctx.Err() != nil:
		err = errors.New("timeout")
	case err == nil && len(out) != len(in):
		err = errors.New("invalid reply: wrong number of translations")
	}
	if err != nil {
		s.log.Warn("localization failed", "job_id", k.job, "language", lang, "reason", err.Error())
		return nil
	}
	return out
}

// cached returns a copy of k's translations (empty when none) and marks k recently used. Caller holds s.mu.
func (s *Service) cached(k key) map[int]string {
	out := map[int]string{}
	if el, ok := s.items[k]; ok {
		s.lru.MoveToFront(el)
		for i, t := range el.Value.(*entry).texts {
			out[i] = t
		}
	}
	return out
}

// store replaces k's translations, evicting the least recently used entry beyond s.max. Caller holds s.mu.
func (s *Service) store(k key, texts map[int]string) {
	if el, ok := s.items[k]; ok {
		el.Value.(*entry).texts = texts
		s.lru.MoveToFront(el)
		return
	}
	s.items[k] = s.lru.PushFront(&entry{k, texts})
	if s.lru.Len() > s.max {
		old := s.lru.Remove(s.lru.Back()).(*entry)
		delete(s.items, old.k)
	}
}

// translationSource is the translation run's source_language when it succeeded, else nil.
func translationSource(j job.Job) *string {
	var tr struct {
		SourceLanguage *string `json:"source_language"`
	}
	if run := succeeded(j, "translation"); run != nil && json.Unmarshal(run.Result, &tr) == nil &&
		tr.SourceLanguage != nil && *tr.SourceLanguage != "" {
		return tr.SourceLanguage
	}
	return nil
}

func succeeded(j job.Job, id string) *job.AnalyzerRun {
	for i := range j.Analyzers {
		if r := &j.Analyzers[i]; r.ID == id && r.Status == job.RunSucceeded && set(r.Result) {
			return r
		}
	}
	return nil
}

// set reports a present, non-null JSON value (a stored job round-trips null as "null").
func set(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}
