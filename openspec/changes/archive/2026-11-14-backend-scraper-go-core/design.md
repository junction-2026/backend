# Design: scraper service in Go

## Decisions

1. **Layout.** `cmd/scraper` holds `main.go` (flags, config, server, shutdown) and `platforms.go`. The core lives in
   `internal/socialscraper` (config, server, auth, validation, errors, model, urls, text, registry, logging, http
   client); each platform module is a subpackage that exports `New(*http.Client) *Module`.
2. **Module interface.** `Module.Scrape(ctx, Target, Request) (*Result, error)` with `Target{Platform, URLKind, URL,
   IDs}`. Modules return `*socialscraper.Error` (constructors exist for every shared error code); a context
   deadline error becomes 504 and any other error 500. The server sets `scraped_at` and `latency_ms` and turns nil
   slices into `[]`.
3. **Registration.** `socialscraper.Register(platform, module)` fills a map. `platforms.go` keeps a list of init
   functions that run after the configuration is read (so modules receive the timeout-bound client); platform
   tasks append to it from their own files (`platforms_meta.go`, `platforms_fbx.go`) and never edit `platforms.go`.
4. **URL recognition is central** (`urls.go`) for all six platforms, so a platform without a module answers 503
   `platform_not_configured` with `details.platform`, never 500. Facebook `profile.php` answers 422
   `unsupported_account_type` from the recognizer, so it does not depend on an installed module.
5. **Request validation.** The body is decoded into `map[string]json.RawMessage`; each member is checked for its
   JSON type (strings, numbers with an integral value) and all offending members are reported in `details.fields`.
6. **Timeouts.** Each scrape runs in its own goroutine under `context.WithTimeout`; the handler waits on the result
   or the deadline, so a module that ignores its context still yields 504. A recovered panic is logged with its
   stack (redacted) and answers 500 with a fixed message.
7. **Redaction.** One `slog` JSON handler whose `ReplaceAttr` passes every string, error and stack attribute
   through a `Redactor` built at start from the environment (token, X cookies, Instagram session path and the
   file's contents). `Bearer <...>` values are replaced everywhere. The access log line has no header fields.
8. **HTTP clients.** `socialscraper.NewClient(timeout, rt)` sets `Client.Timeout` and caps response bodies at 16 MiB
   through a transport wrapper. The Mastodon module copies the injected client and forbids redirects.
9. **Tests.** `scrapertest.Router` is an `http.RoundTripper` over per-host `http.Handler`s that fails the test for
   any unrouted host; modules take an injected `*http.Client`, and nothing uses `http.DefaultTransport` against a
   real host.
10. **Image.** `scraper.Dockerfile`: `golang:1.26-alpine` build stage (`CGO_ENABLED=0`), runtime
    `debian:bookworm-slim` with `chromium ca-certificates fonts-liberation` (`--no-install-recommends`), non-root
    user, `HEALTHCHECK` through `scraper -healthcheck`. The built image was checked for the absence of any
    interpreter, so the digest-pinned headless-shell fallback was not needed.

## Risks

- The canonical specs `scraper-meta-platforms` and `scraper-facebook-x` still describe the removed implementation
  until the follow-up tasks modify them; they are not changed here.
- The Debian base image has a compiler-runtime data directory under `/usr/share/gcc/` named like the interpreter, so
  the CI check looks for interpreter executables and `/usr/lib/<interpreter>3*`, not for any path with that name.
