package socialscraper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// Server serves GET /health and POST /scrape.
type Server struct {
	cfg Config
	reg *Registry
	log *slog.Logger
	now func() time.Time
}

// NewServer builds the handler. reg nil means Default; log nil discards logs.
func NewServer(cfg Config, reg *Registry, log *slog.Logger) http.Handler {
	if reg == nil {
		reg = Default
	}
	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Server{cfg: cfg, reg: reg, log: log, now: time.Now}
}

// reqInfo is filled by the scrape handler for the access log line.
type reqInfo struct{ platform, kind string }

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (s *Server) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	start := s.now()
	w := &statusWriter{ResponseWriter: rw, status: http.StatusOK}
	info := &reqInfo{}
	switch {
	case r.URL.Path == "/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case r.URL.Path == "/scrape" && r.Method == http.MethodPost:
		s.scrape(w, r, info)
	case r.URL.Path == "/health" || r.URL.Path == "/scrape":
		w.Header().Set("Allow", map[string]string{"/health": "GET", "/scrape": "POST"}[r.URL.Path])
		s.writeError(w, &Error{Code: "invalid_request", Message: "method not allowed", Status: http.StatusMethodNotAllowed})
	default:
		s.writeError(w, &Error{Code: "invalid_request", Message: "not found", Status: http.StatusNotFound})
	}
	// method, path, status, platform, url_kind and latency only: never request headers
	s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", w.status,
		"platform", info.platform, "url_kind", info.kind, "latency_ms", s.now().Sub(start).Milliseconds())
}

func (s *Server) scrape(w http.ResponseWriter, r *http.Request, info *reqInfo) {
	// Auth first: the body is not read and no platform is contacted for a bad token.
	if !authorized(r.Header.Get("Authorization"), s.cfg.Token) {
		s.writeError(w, Unauthorized())
		return
	}
	started := s.now()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		s.writeError(w, InvalidRequest("the request body could not be read or is too large"))
		return
	}
	rawURL, req, perr := parseBody(body)
	if perr != nil {
		s.writeError(w, perr)
		return
	}
	target, rerr := Recognize(rawURL)
	if rerr != nil {
		s.writeError(w, rerr)
		return
	}
	info.platform, info.kind = target.Platform, target.URLKind
	mod, ok := s.reg.Lookup(target.Platform)
	if !ok {
		s.writeError(w, PlatformNotConfigured(target.Platform))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.Timeout)
	defer cancel()
	res, merr := s.run(ctx, mod, target, req)
	if merr != nil {
		s.writeError(w, s.mapError(merr, target.Platform))
		return
	}
	if res == nil {
		s.writeError(w, InternalError(target.Platform))
		return
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
	writeJSON(w, http.StatusOK, res)
}

type outcome struct {
	res *Result
	err error
}

// run calls the module in its own goroutine so the timeout holds even for a module that ignores ctx, and
// recovers a panic (the stack goes to the redacted log only).
func (s *Server) run(ctx context.Context, m Module, t Target, req Request) (*Result, error) {
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

func (s *Server) mapError(err error, platform string) *Error {
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
	}
	s.log.Error("scrape failed", "platform", platform, "error", err.Error()) // the message is not sent to the client
	return InternalError(platform)
}

func (s *Server) writeError(w http.ResponseWriter, e *Error) {
	for k, v := range e.Header {
		w.Header()[k] = v
	}
	writeJSON(w, e.Status, e.Envelope())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":{"code":"internal_error","message":"unexpected internal error"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
