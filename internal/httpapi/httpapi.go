// Package httpapi serves the REST jobs API (api-conventions, analysis-jobs-api).
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/junction-2026/backend/internal/engine"
	"github.com/junction-2026/backend/internal/events"
	"github.com/junction-2026/backend/internal/job"
	"github.com/junction-2026/backend/internal/localization"
	"github.com/junction-2026/backend/internal/registry"
	"github.com/junction-2026/backend/internal/store"
)

// Service is the part of the engine the API needs; *engine.Engine implements it.
type Service interface {
	Create(req engine.CreateRequest) (job.Job, error)
	Get(id string) (job.Job, error)
	Page(status string, after *store.Cursor, limit int) ([]job.Job, error)
	Cancel(id string) (job.Job, error)
	Subscribe(id string) (*events.Subscription, error)
	MapCountries() ([]store.MapCountry, error)
	MapAnalyses(code string, after *store.Cursor, limit int) ([]store.MapItem, error)
}

// Config wires the handler.
type Config struct {
	Service  Service
	Registry *registry.Registry
	// Events serves GET /api/v1/jobs/{id}/events (the job id is r.PathValue("id")).
	Events http.Handler
	// Localizer serves GET /api/v1/jobs/{id}/localization; nil: no translation provider.
	Localizer *localization.Service
	Logger    *slog.Logger // default slog.Default()
}

const (
	maxBody  = 1 << 20 // 1 MiB
	basePath = "/api/v1"
)

type api struct {
	svc Service
	reg *registry.Registry
	loc *localization.Service
	log *slog.Logger
}

// NewHandler returns the complete HTTP handler: /api/v1 routes and /healthz.
func NewHandler(c Config) http.Handler {
	a := &api{svc: c.Service, reg: c.Registry, loc: c.Localizer, log: c.Logger}
	if a.log == nil {
		a.log = slog.Default()
	}
	if a.loc == nil {
		a.loc = localization.New(nil, 0, a.log)
	}
	mux := http.NewServeMux()
	route := func(pattern string, methods map[string]http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			h := methods[r.Method]
			if h == nil {
				allow := make([]string, 0, len(methods))
				for m := range methods {
					allow = append(allow, m)
				}
				sort.Strings(allow)
				w.Header().Set("Allow", strings.Join(allow, ", "))
				WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
				return
			}
			h(w, r)
		})
	}
	route("/healthz", map[string]http.HandlerFunc{"GET": func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}})
	route(basePath+"/jobs", map[string]http.HandlerFunc{"POST": a.createJob, "GET": a.listJobs})
	route(basePath+"/jobs/{id}", map[string]http.HandlerFunc{"GET": a.getJob})
	route(basePath+"/jobs/{id}/cancel", map[string]http.HandlerFunc{"POST": a.cancelJob})
	route(basePath+"/jobs/{id}/localization", map[string]http.HandlerFunc{"GET": a.getLocalization})
	route(basePath+"/map/countries", map[string]http.HandlerFunc{"GET": a.mapCountries})
	route(basePath+"/map/countries/{country_code}/analyses", map[string]http.HandlerFunc{"GET": a.mapAnalyses})
	route(basePath+"/analyzers", map[string]http.HandlerFunc{"GET": a.listAnalyzers})
	if c.Events != nil {
		route(basePath+"/jobs/{id}/events", map[string]http.HandlerFunc{"GET": c.Events.ServeHTTP})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		WriteError(w, http.StatusNotFound, "not_found", "no such endpoint", nil)
	})
	return a.middleware(mux)
}

// statusWriter records the status for the request log. It forwards Hijack
// (WebSocket upgrade) and Flush.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (Hijack, Flush).
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// middleware recovers handler panics into 500 and logs one line per request:
// method, path and status only (no query, headers or body).
func (a *api) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				a.log.Error("handler panic", "method", r.Method, "path", r.URL.Path)
				if sw.status == http.StatusOK {
					WriteError(sw, http.StatusInternalServerError, "internal_error", "internal error", nil)
				}
			}
			if r.URL.Path != "/healthz" {
				a.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(start).Milliseconds())
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

type errorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// WriteError writes the api-conventions error envelope.
func WriteError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]errorBody{"error": {Code: code, Message: message, Details: details}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b, status = []byte(`{"error":{"code":"internal_error","message":"internal error"}}`), http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}
