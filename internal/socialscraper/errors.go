package socialscraper

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
)

// Error is a typed, user-visible failure. Message must never carry credentials or stack traces.
type Error struct {
	Code    string
	Message string
	Status  int
	Details map[string]any
	Header  http.Header
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Envelope is the error response body {"error": {"code", "message", "details"?}}.
func (e *Error) Envelope() map[string]any {
	err := map[string]any{"code": e.Code, "message": e.Message}
	if len(e.Details) > 0 {
		err["details"] = e.Details
	}
	return map[string]any{"error": err}
}

func newError(code string, status int, msg, platform string) *Error {
	e := &Error{Code: code, Message: msg, Status: status, Details: map[string]any{}}
	if platform != "" {
		e.Details["platform"] = platform
	}
	return e
}

func Unauthorized() *Error {
	e := newError("unauthorized", http.StatusUnauthorized, "missing or invalid bearer token", "")
	e.Header = http.Header{"Www-Authenticate": {"Bearer"}}
	return e
}

func InvalidRequest(msg string) *Error {
	return newError("invalid_request", http.StatusBadRequest, msg, "")
}

// ValidationFailed maps each offending request member to a message.
func ValidationFailed(fields map[string]string) *Error {
	e := newError("validation_failed", http.StatusUnprocessableEntity, "the request body is invalid", "")
	e.Details["fields"] = fields
	return e
}

func UnsupportedURL(platform, msg string) *Error {
	return newError("unsupported_url", http.StatusUnprocessableEntity, msg, platform)
}

func UnsupportedAccountType(platform, accountType, msg string) *Error {
	e := newError("unsupported_account_type", http.StatusUnprocessableEntity, msg, platform)
	e.Details["account_type"] = accountType
	return e
}

func AccountNotFound(platform, msg string) *Error {
	return newError("account_not_found", http.StatusNotFound, msg, platform)
}

func PostNotFound(platform, msg string) *Error {
	return newError("post_not_found", http.StatusNotFound, msg, platform)
}

func AccountPrivate(platform, msg string) *Error {
	return newError("account_private", http.StatusForbidden, msg, platform)
}

// RateLimited carries Retry-After and details.retry_after_s when retryAfterSeconds is >= 0 (-1: unknown).
func RateLimited(platform string, retryAfterSeconds int) *Error {
	e := newError("rate_limited", http.StatusTooManyRequests, "the platform throttled the scraper", platform)
	if retryAfterSeconds >= 0 {
		e.Details["retry_after_s"] = retryAfterSeconds
		e.Header = http.Header{"Retry-After": {strconv.Itoa(retryAfterSeconds)}}
	}
	return e
}

// PlatformNotConfigured lists the names (never values) of the missing settings.
func PlatformNotConfigured(platform string, missing ...string) *Error {
	e := newError("platform_not_configured", http.StatusServiceUnavailable, "the platform is not configured on this scraper", platform)
	if len(missing) > 0 {
		e.Details["missing_settings"] = missing
	}
	return e
}

func UpstreamError(platform, msg string) *Error {
	return newError("upstream_error", http.StatusBadGateway, msg, platform)
}

func UpstreamTimeout(platform string) *Error {
	return newError("upstream_timeout", http.StatusGatewayTimeout, "the platform did not answer in time", platform)
}

func InternalError(platform string) *Error {
	return newError("internal_error", http.StatusInternalServerError, "unexpected internal error", platform)
}

// IsTimeout reports whether err is a deadline or network timeout.
func IsTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
}
