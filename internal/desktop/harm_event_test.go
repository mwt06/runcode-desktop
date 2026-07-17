package desktop

import (
	"testing"

	"github.com/wt68/runcode/engine/permissions"
)

// A harm-gate audit event must reach the frontend as an EventHarmAutoAllow with
// a sanitized HarmAutoAllow payload. harmAuditFunc is the exact closure
// configureSession installs as the permission service's Audit sink, fed here
// with a recording emitter in place of the session's envelope emitter.
// (Envelope wrapping/sequencing is the host emitter's contract, covered by
// internal/host's tests; the pre-host version of this test asserted it via the
// App-wide envelope sink.)
func TestHarmAuditFuncEmitsSanitizedEvent(t *testing.T) {
	type emitted struct {
		event   string
		payload any
	}
	var events []emitted
	audit := harmAuditFunc(func(event string, payload any) {
		events = append(events, emitted{event, payload})
	})

	audit(permissions.HarmAuditEvent{
		ToolName:       "Bash",
		ToolUseID:      "toolu_abc",
		Operation:      permissions.OperationExecute,
		Risk:           permissions.RiskHigh,
		Reason:         "看着安全",
		Outcome:        permissions.HarmGateAutoAllowed,
		AutoAllowCount: 3,
	})

	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events))
	}
	if events[0].event != EventHarmAutoAllow {
		t.Fatalf("event = %q, want %q", events[0].event, EventHarmAutoAllow)
	}
	payload, ok := events[0].payload.(HarmAutoAllow)
	if !ok {
		t.Fatalf("payload type = %T, want HarmAutoAllow", events[0].payload)
	}
	if payload.Tool != "Bash" || payload.ToolUseID != "toolu_abc" || payload.Operation != "execute" || payload.Outcome != "auto_allowed" || payload.Reason != "看着安全" || payload.Count != 3 {
		t.Fatalf("payload = %+v, want mapped fields (tool/toolUseID/operation/outcome/reason/count)", payload)
	}
}
