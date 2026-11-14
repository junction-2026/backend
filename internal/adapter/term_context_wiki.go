package adapter

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// termQuoteStoreMax bounds the first lead sentence kept in the cache; quote_max_chars is applied when it is read.
const termQuoteStoreMax = 2000

// termSearchHits is how many search hits one request returns (and extracts the lead of) when the title has no page.
const termSearchHits = "5"

// termLookup answers one term from this run, the disk cache or Wikipedia (see lookup for the claim variant). fromCache
// is true when no request was sent for it; ok is false after a failed request (the run then fails as a whole).
func (r *wikiRun) termLookup(f *wikiFlight, leader bool, lang, term string) (l wikiLookup, fromCache, ok bool) {
	if !leader {
		select {
		case <-f.done:
		case <-r.ctx.Done():
			return wikiLookup{}, false, false
		}
		return f.l, true, !f.failed
	}
	defer close(f.done)
	if l, ok := r.cache.get(f.key); ok {
		f.l = l
		return l, true, true
	}
	l, err := r.fetchTerm(lang, term)
	if err != nil {
		f.failed = true
		r.setFatal(err)
		return wikiLookup{}, false, false
	}
	f.l = l
	r.cache.put(f.key, l)
	return l, false, true
}

// fetchTerm finds the term's own page: the page of the title (redirects followed) or, when there is none, the first
// search hit whose title is the term or "term (qualifier)". A disambiguation page, no page or an empty lead is "no_page".
func (r *wikiRun) fetchTerm(lang, term string) (wikiLookup, error) {
	l := wikiLookup{Language: lang, Query: term, Status: "no_page"}
	props := url.Values{"action": {"query"}, "prop": {"pageprops|revisions|extracts"}, "ppprop": {"disambiguation"},
		"rvprop": {"ids|timestamp"}, "exintro": {"1"}, "explaintext": {"1"}, "exlimit": {"max"}}
	var page *wikiPage
	if !strings.ContainsAny(term, "|#<>[]{}\n") { // characters MediaWiki titles cannot hold (or splits on)
		props.Set("titles", term)
		props.Set("redirects", "1")
		pages, err := r.termPages(lang, props)
		if err != nil {
			return l, err
		}
		for i := range pages {
			if p := &pages[i]; p.PageID > 0 && !p.Missing && p.Ns == 0 {
				page = p
			}
		}
		props.Del("titles")
		props.Del("redirects")
	}
	if page == nil {
		props.Set("generator", "search")
		props.Set("gsrsearch", term)
		props.Set("gsrnamespace", "0")
		props.Set("gsrlimit", termSearchHits)
		pages, err := r.termPages(lang, props)
		if err != nil {
			return l, err
		}
		for i := range pages {
			if p := &pages[i]; p.PageID > 0 && !p.Missing && p.Ns == 0 && entityTitle(p.Title, []string{term}) && (page == nil || p.Index < page.Index) {
				page = p
			}
		}
	}
	if page == nil {
		return l, nil
	}
	if _, dis := page.PageProps["disambiguation"]; dis || strings.TrimSpace(page.Extract) == "" {
		return l, nil
	}
	if len(page.Revisions) == 0 {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	at, err := time.Parse(time.RFC3339, page.Revisions[0].Timestamp)
	if err != nil || page.Revisions[0].RevID < 1 {
		return l, wikiErr(lang, "answer could not be decoded")
	}
	rev, revised := page.Revisions[0].RevID, at.UTC().Format(time.RFC3339)
	link, passage, source := revisionURL(lang, page.Title, rev), cutRunes(leadSentence(page.Extract), termQuoteStoreMax), "lead"
	l.Status, l.Title, l.URL, l.PageID, l.RevisionID, l.RevisedAt = "found", &page.Title, &link, &page.PageID, &rev, &revised
	l.Passage, l.PassageSource = &passage, &source
	return l, nil
}

// termPages sends one MediaWiki query and returns its pages (none when the answer has no query member).
func (r *wikiRun) termPages(lang string, v url.Values) ([]wikiPage, error) {
	body, err := r.get(lang, v)
	if err != nil {
		return nil, err
	}
	var a struct {
		Query *struct {
			Pages []wikiPage `json:"pages"`
		} `json:"query"`
	}
	if json.Unmarshal(body, &a) != nil {
		return nil, wikiErr(lang, "answer could not be decoded")
	}
	if a.Query == nil {
		return nil, nil
	}
	return a.Query.Pages, nil
}
