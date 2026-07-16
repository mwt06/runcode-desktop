// Package sessions persists the full, loss-less conversation history of a
// session so it can be resumed across processes. Unlike the transcript package
// (which stores a sanitized summary), each record here is a complete llm.Message
// and is intended for reconstruction, not auditing.
package sessions

import (
	"context"

	"github.com/wt68/runcode/engine/llm"
)

// Store appends complete conversation messages for a session. Implementations
// must be safe for concurrent Append.
type Store interface {
	// Append writes the given messages to the session's history, in order.
	// One Append call is one atomic, durable commit unit: callers commit a
	// whole turn per call, and implementations persist it all-or-nothing —
	// concurrent Appends may interleave between calls but never within one.
	Append(ctx context.Context, messages []llm.Message) error
	// Close releases any underlying resources.
	Close(ctx context.Context) error
}

type noopStore struct{}

// Noop returns a Store that discards everything (session persistence disabled).
func Noop() Store { return noopStore{} }

func (noopStore) Append(context.Context, []llm.Message) error { return nil }

func (noopStore) Close(context.Context) error { return nil }
