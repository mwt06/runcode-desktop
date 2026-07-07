package desktop

import (
	"testing"

	"github.com/wt68/runcode/internal/permissions"
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
	payload, ok := ev.data.(HarmAutoAllow)
	if !ok {
		t.Fatalf("event payload type = %T, want HarmAutoAllow", ev.data)
	}
	if payload.Tool != "Bash" || payload.ToolUseID != "toolu_abc" || payload.Operation != "execute" || payload.Outcome != "auto_allowed" || payload.Reason != "看着安全" {
		t.Fatalf("payload = %+v, want mapped fields (tool/toolUseID/operation/outcome/reason)", payload)
	}
}
