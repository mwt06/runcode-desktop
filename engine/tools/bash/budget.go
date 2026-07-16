package bash

import "sync/atomic"

// Budget is an optional cross-session cap on background shells, composed on top
// of each Manager's per-session limit. A server host shares one Budget across
// every session's Manager so the process-wide number of background shells stays
// bounded no matter how many sessions run. TryAcquire fails fast (the model can
// kill a shell and retry), matching the per-session limit's semantics — a
// blocking acquire would stall a tool call on another session's shells.
//
// All methods are nil-safe: a nil *Budget means "no global cap", so Manager
// code paths need no conditional wiring.
type Budget struct {
	max  int64
	used atomic.Int64
}

// NewBudget returns a Budget allowing at most max concurrently running
// background shells. max <= 0 yields a budget that admits nothing (callers
// wanting "no cap" pass a nil *Budget instead).
func NewBudget(max int) *Budget {
	return &Budget{max: int64(max)}
}

// TryAcquire claims one slot, reporting false when the budget is exhausted.
// Each successful acquire must be paired with exactly one Release.
func (b *Budget) TryAcquire() bool {
	if b == nil {
		return true
	}
	if b.used.Add(1) > b.max {
		b.used.Add(-1)
		return false
	}
	return true
}

// Release returns a slot claimed by a successful TryAcquire.
func (b *Budget) Release() {
	if b == nil {
		return
	}
	b.used.Add(-1)
}

// Max reports the budget's cap, for error messages.
func (b *Budget) Max() int {
	if b == nil {
		return 0
	}
	return int(b.max)
}
