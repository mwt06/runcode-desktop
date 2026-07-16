package permissions

import "sync"

// This file adds judge ("smart") mode's audit + circuit breaker: an observable
// record of every model-driven auto-allow, and a per-session cap so a fooled or
// compromised harm judge cannot silently wave through an unbounded stream of
// risky actions. Both sit on the harm gate (the model-driven auto-allow); the
// deterministic judge-mode workspace mutation is out of scope here.

// defaultHarmAutoAllowLimit bounds how many risky actions the harm gate may
// auto-allow in one session before it trips back to prompting. Tunable via
// NewHarmBreaker; this is the fallback when no explicit limit is given.
const defaultHarmAutoAllowLimit = 50

// HarmGateOutcome is what the harm gate did with an action it was consulted on.
type HarmGateOutcome string

const (
	// HarmGateAutoAllowed means the model judged the action safe and the breaker
	// permitted auto-allowing it without a prompt.
	HarmGateAutoAllowed HarmGateOutcome = "auto_allowed"
	// HarmGateBreakerTripped means the model judged the action safe but the session
	// auto-allow budget was exhausted, so it was escalated to a prompt instead.
	HarmGateBreakerTripped HarmGateOutcome = "breaker_tripped"
)

// HarmAuditEvent records a harm-gate decision for the host to surface, so the user
// can review what smart mode decided without asking. It carries only sanitized
// fields (no raw command or path), so it is safe to emit to telemetry as well.
type HarmAuditEvent struct {
	ToolName       string
	ToolUseID      string
	Operation      Operation
	Risk           Risk
	Reason         string
	Outcome        HarmGateOutcome
	AutoAllowCount int
}

// HarmAuditFunc receives harm-gate audit events. It must be safe for concurrent
// use, since actions may be authorized from parallel goroutines.
type HarmAuditFunc func(HarmAuditEvent)

// HarmBreaker caps the number of harm-gate auto-allows in a session. It is safe
// for concurrent use. A nil *HarmBreaker allows everything (the bound is off).
type HarmBreaker struct {
	limit int

	mu    sync.Mutex
	count int
}

// NewHarmBreaker returns a breaker permitting at most limit auto-allows. A limit
// <= 0 uses defaultHarmAutoAllowLimit.
func NewHarmBreaker(limit int) *HarmBreaker {
	if limit <= 0 {
		limit = defaultHarmAutoAllowLimit
	}
	return &HarmBreaker{limit: limit}
}

// Allow reports whether the harm gate may auto-allow one more action, counting it
// when permitted. Once the session limit is reached it returns false for the rest
// of the session. A nil breaker always allows.
func (b *HarmBreaker) Allow() bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count >= b.limit {
		return false
	}
	b.count++
	return true
}

// Count returns how many actions have been auto-allowed so far.
func (b *HarmBreaker) Count() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// Tripped reports whether the auto-allow budget is exhausted.
func (b *HarmBreaker) Tripped() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count >= b.limit
}
