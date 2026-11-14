package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/registry"
)

const termNoCoverage = "no_coverage"

type (
	termQuoteRef struct {
		Source string `json:"source"`
		Index  *int   `json:"index"`
	}
	// termEncyclopedia is the shared EncyclopediaQuote.
	termEncyclopedia struct {
		Quote      string `json:"quote"`
		Title      string `json:"title"`
		URL        string `json:"url"`
		Language   string `json:"language"`
		PageID     int64  `json:"page_id"`
		RevisionID int64  `json:"revision_id"`
	}
	// termElsewhere is the shared ElsewhereQuote.
	termElsewhere struct {
		Snippet string `json:"snippet"`
		URL     string `json:"url"`
		Title   string `json:"title"`
		Domain  string `json:"domain"`
	}
	// termEntry is the shared TermContext; Encyclopedia is a *termEncyclopedia or the string termNoCoverage.
	termEntry struct {
		Term         string          `json:"term"`
		InPost       string          `json:"in_post"`
		Encyclopedia any             `json:"encyclopedia"`
		Elsewhere    []termElsewhere `json:"elsewhere"`
		Sense        string          `json:"sense"`
		DiffersFrom  []termQuoteRef  `json:"differs_from"`
	}
	termResult struct {
		Language     string          `json:"language"`
		Terms        []termEntry     `json:"terms"`
		RequestsUsed int64           `json:"requests_used"`
		Cached       int             `json:"cached"`
		Usage        json.RawMessage `json:"usage"`
	}
)

// termContext picks loaded or unfamiliar terms of the original text with an LLM, keeps those that occur verbatim,
// quotes the post's sentence, the term's own Wikipedia lead sentence and news_search snippets, and has the LLM say
// only whether the quotes use the term for the same thing. No model-written text enters the result.
func (x *Executor) termContext(ctx context.Context, def *registry.Analyzer, req Request) (json.RawMessage, error) {
	e := &def.Execution
	var ext struct {
		Claims []struct {
			Entities []string `json:"entities"`
		} `json:"claims"`
		InputTruncated bool `json:"input_truncated"`
	}
	var news struct {
		Articles []struct {
			Title, URL, Domain string
			Snippet            *string
		} `json:"articles"`
	}
	if !decodeInput(req.Inputs["extraction"], &ext) || !decodeInput(req.Inputs["news"], &news) {
		return nil, adapterErr("a dependency result is not a JSON object")
	}
	content, _ := req.Inputs[jobContentKey].(string)
	if strings.TrimSpace(content) == "" {
		return nil, adapterErr("job input has no content to analyze")
	}
	text := []rune(content)
	if ext.InputTruncated && len(text) > e.MaxChars() { // the part topic_extraction analysed
		text = []rune(cutAtSpace(text, e.MaxChars()))
	}
	lang := TermContextLanguage(req.Inputs)
	red := llmTechniquesRedactor(e)
	fail := func(err error) (json.RawMessage, error) {
		if re, ok := err.(*job.RunError); ok {
			re.Message = red(re.Message)
		}
		return nil, err
	}

	var cands []string
	seen := map[string]bool{}
	for _, c := range ext.Claims {
		for _, s := range c.Entities {
			if s = strings.TrimSpace(s); s != "" && !seen[fold(s)] {
				seen[fold(s)] = true
				cands = append(cands, s)
			}
		}
	}
	var picks []string
	sel, err := llmChat(ctx, def, termSelectionPrompt, termSelectionMessage(e, string(text), cands, e.MaxTermsN()), termSelectionSchema, "term_selection",
		func(raw json.RawMessage) (err error) { picks, err = parseTermSelection(raw); return err })
	if err != nil {
		return fail(withPrefix(err, "invalid term selection reply: "))
	}
	usage := sel.Usage

	terms := keepTerms(text, picks, e.MaxTermsN(), e.InPostMaxCharsN())
	res := termResult{Language: lang, Terms: []termEntry{}}
	if len(terms) > 0 {
		pages, requests, cached, err := x.lookupTerms(ctx, e, lang, terms)
		if err != nil {
			return fail(err)
		}
		res.RequestsUsed, res.Cached = requests, cached
		for i, t := range terms {
			it := termEntry{Term: t.term, InPost: t.inPost, Encyclopedia: termNoCoverage, Elsewhere: []termElsewhere{}, Sense: "unknown", DiffersFrom: []termQuoteRef{}}
			if q := pages[i]; q != nil {
				it.Encyclopedia = q
			}
			pat := []rune(t.term)
			for _, a := range news.Articles {
				if len(it.Elsewhere) >= e.ElsewhereMaxN() {
					break
				}
				if a.Snippet != nil {
					if _, ok := findTerm([]rune(*a.Snippet), pat); ok {
						it.Elsewhere = append(it.Elsewhere, termElsewhere{*a.Snippet, a.URL, a.Title, a.Domain})
					}
				}
			}
			res.Terms = append(res.Terms, it)
		}
		usage2, err := compareTerms(ctx, def, res.Terms)
		if err != nil {
			return fail(err)
		}
		usage = mergeUsage(usage, usage2)
	}
	res.Usage = usage
	out, err := json.Marshal(res)
	if err != nil {
		return nil, adapterErr("term_context result is not serializable")
	}
	return out, nil
}

// decodeInput reads a dependency result (any JSON value of the Inputs) into v; false when it is not a JSON object.
func decodeInput(in any, v any) bool {
	b, _ := json.Marshal(in)
	return len(b) > 0 && b[0] == '{' && json.Unmarshal(b, v) == nil
}

func withPrefix(err error, prefix string) error {
	if re, ok := err.(*job.RunError); ok && re.Code == job.CodeInvalidOutput {
		re.Message = prefix + re.Message
	}
	return err
}

// parseTermSelection reads {"terms": [string]}; messages never quote the reply.
func parseTermSelection(raw json.RawMessage) ([]string, error) {
	var in struct {
		Terms *[]*string `json:"terms"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Terms == nil {
		return nil, errors.New("reply is not {terms: [string]}")
	}
	out := make([]string, 0, len(*in.Terms))
	for i, s := range *in.Terms {
		if s == nil {
			return nil, fmt.Errorf("terms[%d] is not a string", i)
		}
		out = append(out, *s)
	}
	return out, nil
}

type keptTerm struct {
	term, inPost string
	pos          int // rune index of the first occurrence
}

// keepTerms applies "Kept terms", "Kept term limit and order": a pick is kept when it has a letter, occurs in text with
// letter/digit boundaries (case-insensitively) and fits in_post_max_chars; duplicates (case-insensitive) are dropped,
// at most max are kept in the model's order, then ordered by first occurrence.
func keepTerms(text []rune, picks []string, max, inPostMax int) []keptTerm {
	var out []keptTerm
	seen := map[string]bool{}
	for _, p := range picks {
		p = strings.TrimSpace(p)
		pat := []rune(p)
		if len(out) >= max || !hasLetter(p) || len(pat) > inPostMax || seen[fold(p)] {
			continue
		}
		if s, ok := findTerm(text, pat); ok {
			seen[fold(p)] = true
			out = append(out, keptTerm{string(text[s : s+len(pat)]), sentenceAround(text, s, s+len(pat), inPostMax), s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}

// lookupTerms resolves every term's own page concurrently (a nil entry is no_coverage), sharing the wikipedia
// adapter's client, concurrency limit and cache. The first failed request fails the lookup.
func (x *Executor) lookupTerms(pctx context.Context, e *registry.Execution, lang string, terms []keptTerm) (pages []*termEncyclopedia, requests int64, cached int, err error) {
	ctx, cancel := context.WithCancel(pctx)
	defer cancel()
	var fatal error
	var once sync.Once
	r := &wikiRun{ctx: ctx, cfg: e.Wiki, x: x, cache: wikiCache{e.Wiki.CacheDir, e.Wiki.CacheTTL}, sem: make(chan struct{}, e.Wiki.MaxConcurrency),
		flights: map[string]*wikiFlight{}, setFatal: func(err error) {
			if _, ok := err.(*job.RunError); ok { // a context error is judged after wg.Wait
				once.Do(func() { fatal = err })
				cancel()
			}
		}}
	pages = make([]*termEncyclopedia, len(terms))
	hit := make([]bool, len(terms))
	var wg sync.WaitGroup
	for i, t := range terms {
		f, leader := r.flight(wikiKey(lang, t.term, "term"))
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, fromCache, ok := r.termLookup(f, leader, lang, t.term)
			hit[i] = fromCache
			if ok && l.Status == "found" {
				pages[i] = &termEncyclopedia{cutQuote(*l.Passage, e.QuoteMaxCharsN()), *l.Title, *l.URL, lang, *l.PageID, *l.RevisionID}
			}
		}()
	}
	wg.Wait()
	if err := ContextError(pctx); err != nil {
		return nil, 0, 0, err
	}
	if fatal != nil {
		return nil, 0, 0, fatal
	}
	for _, h := range hit {
		if h {
			cached++
		}
	}
	return pages, r.requests.Load(), cached, nil
}

// cutQuote cuts a quote to a leading part of at most n runes.
func cutQuote(q string, n int) string { return strings.TrimRight(cutRunes(q, n), " \t\r\n") }

// compareTerms asks one LLM request whether the post uses each term (that has another quote) for the same thing as
// the other quotes, and sets sense and differs_from. Terms without another quote stay unknown, without a call.
func compareTerms(ctx context.Context, def *registry.Analyzer, terms []termEntry) (json.RawMessage, error) {
	var items []termCompareItem
	var idx []int
	for i, t := range terms {
		q, hasEnc := t.Encyclopedia.(*termEncyclopedia)
		if !hasEnc && len(t.Elsewhere) == 0 {
			continue
		}
		it := termCompareItem{Term: t.Term, InPost: t.InPost, Elsewhere: []string{}}
		if hasEnc {
			it.Encyclopedia = &q.Quote
		}
		for _, w := range t.Elsewhere {
			it.Elsewhere = append(it.Elsewhere, w.Snippet)
		}
		items, idx = append(items, it), append(idx, i)
	}
	if len(items) == 0 {
		return nil, nil
	}
	var verdicts []termVerdict
	reply, err := llmChat(ctx, def, termComparisonPrompt, termComparisonMessage(&def.Execution, items), termComparisonSchema, "term_comparison",
		func(raw json.RawMessage) (err error) {
			verdicts, err = parseTermComparison(raw, terms, idx)
			return err
		})
	if err != nil {
		return nil, withPrefix(err, "invalid term comparison reply: ")
	}
	for k, i := range idx {
		terms[i].Sense, terms[i].DiffersFrom = verdicts[k].sense, verdicts[k].differsFrom
	}
	return reply.Usage, nil
}

type termVerdict struct {
	sense       string
	differsFrom []termQuoteRef
}

// parseTermComparison applies "Comparison reply validation" to the compared terms terms[idx[k]] and returns their
// verdicts in that order. Messages never quote the reply.
func parseTermComparison(raw json.RawMessage, terms []termEntry, idx []int) ([]termVerdict, error) {
	var in struct {
		Terms *[]*struct {
			Term, Sense *string
			DiffersFrom *[]*struct {
				Source *string
				Index  *int
			} `json:"differs_from"`
		} `json:"terms"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Terms == nil {
		return nil, errors.New("reply is not {terms: [{term, sense, differs_from}]}")
	}
	byTerm := map[string]int{} // folded term -> position in the reply
	for n, r := range *in.Terms {
		if r == nil || r.Term == nil || r.Sense == nil {
			return nil, fmt.Errorf("terms[%d] needs a string term and sense", n)
		}
		if _, dup := byTerm[fold(strings.TrimSpace(*r.Term))]; !dup {
			byTerm[fold(strings.TrimSpace(*r.Term))] = n
		}
	}
	out := make([]termVerdict, len(idx))
	for k, i := range idx {
		n, ok := byTerm[fold(terms[i].Term)]
		if !ok {
			return nil, fmt.Errorf("terms has no entry for compared term %d", k)
		}
		r := (*in.Terms)[n]
		switch *r.Sense {
		case "same":
			out[k] = termVerdict{"same", []termQuoteRef{}}
		case "differs":
			refs := []termQuoteRef{}
			if r.DiffersFrom != nil {
				for _, d := range *r.DiffersFrom {
					if d != nil && d.Source != nil {
						if ref, ok := resolveQuoteRef(terms[i], *d.Source, d.Index); ok && !containsRef(refs, ref) {
							refs = append(refs, ref)
						}
					}
				}
			}
			if len(refs) == 0 {
				return nil, fmt.Errorf("terms[%d] differs without a resolvable differs_from reference", n)
			}
			sort.SliceStable(refs, func(a, b int) bool { return refRank(refs[a]) < refRank(refs[b]) })
			out[k] = termVerdict{"differs", refs}
		default:
			return nil, fmt.Errorf("terms[%d].sense must be same or differs", n)
		}
	}
	return out, nil
}

// resolveQuoteRef accepts a reference to the term's encyclopedia quote (index null) or to one of its elsewhere items.
func resolveQuoteRef(t termEntry, source string, index *int) (termQuoteRef, bool) {
	switch source {
	case "encyclopedia":
		if _, ok := t.Encyclopedia.(*termEncyclopedia); ok { // a model-given index is ignored: the reference is null
			index = nil
			return termQuoteRef{Source: source}, true
		}
	case "elsewhere":
		if index != nil && *index >= 0 && *index < len(t.Elsewhere) {
			return termQuoteRef{source, index}, true
		}
	}
	return termQuoteRef{}, false
}

func containsRef(refs []termQuoteRef, r termQuoteRef) bool {
	for _, o := range refs {
		if o.Source == r.Source && (o.Index == nil) == (r.Index == nil) && (o.Index == nil || *o.Index == *r.Index) {
			return true
		}
	}
	return false
}

// refRank orders references: the encyclopedia first, then elsewhere by index.
func refRank(r termQuoteRef) int {
	if r.Index == nil {
		return -1
	}
	return *r.Index
}
