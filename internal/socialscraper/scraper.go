package socialscraper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime/debug"
	"time"
)

// Scraper runs scrapes in process: URL recognition, the platform module, panic recovery and error
// mapping. The caller's context bounds a whole scrape; the modules' http.Client bounds each request.
type Scraper struct {
	reg *Registry
	log *slog.Logger
	now func() time.Time
}

// New returns a scraper over reg. log nil discards logs.
func New(reg *Registry, log *slog.Logger) *Scraper {
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Scraper{reg: reg, log: log, now: time.Now}
}

// Scrape scrapes rawURL (an absolute http(s) URL, already validated by the caller). It never panics:
// a failure is always a typed *Error, internal_error included.
func (s *Scraper) Scrape(ctx context.Context, rawURL string, req Request) (*Result, *Error) {
	started := s.now()
	target, rerr := Recognize(rawURL)
	if rerr != nil {
		return nil, rerr
	}
	mod, ok := s.reg.Lookup(target.Platform)
	if !ok {
		return nil, PlatformNotConfigured(target.Platform)
	}
	res, err := s.run(ctx, mod, target, req)
	if err != nil && target.Platform == Mastodon && isUnsupported(err) {
		// The host does not serve the Mastodon REST API: scrape the URL as a web page in the same scrape.
		target = Target{Platform: Web, URLKind: KindPage, URL: target.URL}
		if mod, ok = s.reg.Lookup(Web); !ok {
			return nil, PlatformNotConfigured(Web)
		}
		res, err = s.run(ctx, mod, target, req)
	}
	if err != nil {
		return nil, s.mapError(err, target.Platform)
	}
	if res == nil {
		return nil, InternalError(target.Platform)
	}
	res.ScrapedAt = s.now().UTC().Format("2006-01-02T15:04:05Z")
	res.LatencyMS = s.now().Sub(started).Milliseconds()
	if res.Posts == nil {
		res.Posts = []Post{}
	}
	if res.Comments == nil {
		res.Comments = []Comment{}
	}
	if res.Warnings == nil {
		res.Warnings = []Warning{}
	}
	return res, nil
}

func isUnsupported(err error) bool {
	var se *Error
	return errors.As(err, &se) && se.Code == "unsupported_url"
}

type outcome struct {
	res *Result
	err error
}

// run calls the module in its own goroutine so ctx holds even for a module that ignores it, and
// recovers a panic (the stack goes to the redacted log only).
func (s *Scraper) run(ctx context.Context, m Module, t Target, req Request) (*Result, error) {
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("module panicked", "platform", t.Platform, "panic", p, "stack", string(debug.Stack()))
				done <- outcome{err: errors.New("module panicked")}
			}
		}()
		res, err := m.Scrape(ctx, t, req)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Scraper) mapError(err error, platform string) *Error {
	var se *Error
	switch {
	case errors.As(err, &se):
		// Copy: the module's error may be shared, and its Details may be nil.
		cp := *se
		cp.Details = make(map[string]any, len(se.Details)+1)
		for k, v := range se.Details {
			cp.Details[k] = v
		}
		if _, ok := cp.Details["platform"]; !ok && platform != "" {
			cp.Details["platform"] = platform
		}
		return &cp
	case IsTimeout(err):
		return UpstreamTimeout(platform)
	case errors.Is(err, context.Canceled): // the caller gave up; nothing to log
		return InternalError(platform)
	}
	s.log.Error("scrape failed", "platform", platform, "error", err.Error()) // the message is not returned
	return InternalError(platform)
}
