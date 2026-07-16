package llm

import (
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies a provider failure so callers can react uniformly —
// retry a transient fault, back off on a rate limit, or surface an auth problem
// — without parsing provider-specific error strings.
type ErrorKind string

const (
	ErrorKindUnknown        ErrorKind = "unknown"
	ErrorKindRateLimited    ErrorKind = "rate_limited"    // HTTP 429
	ErrorKindOverloaded     ErrorKind = "overloaded"      // HTTP 529 / transient saturation
	ErrorKindServer         ErrorKind = "server"          // HTTP 5xx
	ErrorKindAuth           ErrorKind = "auth"            // HTTP 401 / 403
	ErrorKindInvalidRequest ErrorKind = "invalid_request" // other 4xx
	ErrorKindTransport      ErrorKind = "transport"       // network / connection failure
)

// Error is a provider-neutral error. Each provider normalizes its SDK/HTTP
// failures into one so upper layers get a stable contract: Kind for
// classification, Retryable for retry decisions, RetryAfter for backoff, and the
// wrapped cause for logging. It is created by providers; callers read it via
// AsError / IsRetryable.
type Error struct {
	Kind       ErrorKind
	Retryable  bool
	RetryAfter time.Duration
	StatusCode int    // HTTP status when known, else 0
	Provider   string // provider name, for diagnostics
	Message    string
	Err        error // underlying cause, if any
}

func (e *Error) Error() string {
	provider := e.Provider
	if provider == "" {
		provider = "llm"
	}
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	} else if e.Err != nil && e.StatusCode == 0 {
		// Transport-level failure (no HTTP status): surface the underlying cause
		// (dial/TLS/timeout/EOF/connection reset) — a bare "request failed" is
		// undiagnosable. Status errors already carry a meaningful message + code.
		if cause := e.Err.Error(); cause != "" && cause != msg {
			msg = msg + ": " + cause
		}
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s %s error (%d): %s", provider, e.Kind, e.StatusCode, msg)
	}
	return fmt.Sprintf("%s %s error: %s", provider, e.Kind, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// AsError extracts a *Error from err's chain, if present.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsRetryable reports whether err carries a provider Error marked retryable.
func IsRetryable(err error) bool {
	e, ok := AsError(err)
	return ok && e.Retryable
}

// ClassifyHTTPStatus maps an HTTP status code to a neutral Kind and whether it is
// typically retryable. Providers share it so the same status classifies the same
// way regardless of backend.
func ClassifyHTTPStatus(code int) (kind ErrorKind, retryable bool) {
	switch {
	case code == 429:
		return ErrorKindRateLimited, true
	case code == 529:
		return ErrorKindOverloaded, true
	case code == 401, code == 403:
		return ErrorKindAuth, false
	case code >= 500:
		return ErrorKindServer, true
	case code >= 400:
		return ErrorKindInvalidRequest, false
	default:
		return ErrorKindUnknown, false
	}
}
