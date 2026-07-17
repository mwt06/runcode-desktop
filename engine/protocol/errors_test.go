package protocol

import (
	"encoding/json"
	"errors"
	"testing"
)

// Error() must serialize the whole structured error as JSON, so a string-only
// transport still carries the code (and details) to the client.
func TestErrorStringIsJSON(t *testing.T) {
	t.Parallel()
	e := &Error{Code: ErrCodeBusy, Message: "a turn is already in progress", Details: map[string]string{"sessionId": "s1"}}
	var round Error
	if err := json.Unmarshal([]byte(e.Error()), &round); err != nil {
		t.Fatalf("Error() is not valid JSON: %v (got %q)", err, e.Error())
	}
	if round.Code != e.Code || round.Message != e.Message || round.Details["sessionId"] != "s1" {
		t.Fatalf("round-tripped error = %+v, want %+v", round, *e)
	}
}

// A zero-value error still yields parseable JSON (empty code/message), never a
// bare string, so client parsing stays uniform.
func TestErrorStringZeroValue(t *testing.T) {
	t.Parallel()
	var e Error
	var round map[string]any
	if err := json.Unmarshal([]byte(e.Error()), &round); err != nil {
		t.Fatalf("zero Error() is not valid JSON: %v (got %q)", err, e.Error())
	}
}

// Sentinel *Error values keep working with errors.Is/errors.As — the Error()
// change is representation only.
func TestErrorSentinelMatching(t *testing.T) {
	t.Parallel()
	sentinel := &Error{Code: ErrCodeNotFound, Message: "session not found"}
	wrapped := errorsJoinLike(sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Fatal("errors.Is lost sentinel identity")
	}
	var pe *Error
	if !errors.As(wrapped, &pe) || pe.Code != ErrCodeNotFound {
		t.Fatalf("errors.As failed to recover the protocol error: %#v", wrapped)
	}
}

// errorsJoinLike wraps err one level deep (like a caller adding context).
func errorsJoinLike(err error) error {
	return wrapErr{err}
}

type wrapErr struct{ err error }

func (w wrapErr) Error() string { return "ctx: " + w.err.Error() }
func (w wrapErr) Unwrap() error { return w.err }
