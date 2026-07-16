package desktop

import (
	"testing"

	"github.com/wt68/runcode/engine/permissions"
	"github.com/wt68/runcode/pkg/protocol"
)

// A harm-gate audit event must reach the frontend as an EventHarmAutoAllow with a
// sanitized HarmAutoAllow payload, so the UI can show what smart mode auto-allowed.
func TestEmitHarmAutoAllowEmitsSanitizedEvent(t *testing.T) {
	sink := &recordingSink{}
	app := New(sink)

	app.emitHarmAutoAllow(permissions.HarmAuditEvent{
		ToolName:       "Bash",
		ToolUseID:      "toolu_abc",
		Operation:      permissions.OperationExecute,
		Risk:           permissions.RiskHigh,
		Reason:         "看着安全",
		Outcome:        permissions.HarmGateAutoAllowed,
		AutoAllowCount: 3,
	})

	ev, ok := sink.lastOf(EventHarmAutoAllow)
	if !ok {
		t.Fatalf("no %q event emitted", EventHarmAutoAllow)
	}
	// App-level emissions are envelope-wrapped; the payload sits inside.
	env, ok := ev.data.(protocol.Envelope)
	if !ok {
		t.Fatalf("event type = %T, want protocol.Envelope", ev.data)
	}
	if env.Event != EventHarmAutoAllow || env.Seq == 0 || env.TS == "" {
		t.Fatalf("envelope = %+v, want event name, non-zero seq, and ts", env)
	}
	payload, ok := env.Payload.(HarmAutoAllow)
	if !ok {
		t.Fatalf("envelope payload type = %T, want HarmAutoAllow", env.Payload)
	}
	if payload.Tool != "Bash" || payload.ToolUseID != "toolu_abc" || payload.Operation != "execute" || payload.Outcome != "auto_allowed" || payload.Reason != "看着安全" {
		t.Fatalf("payload = %+v, want mapped fields (tool/toolUseID/operation/outcome/reason)", payload)
	}
}
