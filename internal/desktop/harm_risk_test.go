package desktop

import "testing"

// The harm-judge risk tier maps to the gate decision: none/low auto-allow, the rest
// (including an unknown tier) prompt.
func TestHarmRiskPromptsMapping(t *testing.T) {
	t.Parallel()
	for _, r := range []string{"none", "low"} {
		if harmRiskPrompts(r) {
			t.Errorf("risk %q should auto-allow (not prompt)", r)
		}
	}
	for _, r := range []string{"medium", "high", "critical", "weird", ""} {
		if !harmRiskPrompts(r) {
			t.Errorf("risk %q should prompt", r)
		}
	}
}

// The tier is folded into the reason so the UI shows the severity.
func TestLabelHarmReason(t *testing.T) {
	t.Parallel()
	if got := labelHarmReason("high", "会删数据"); got != "风险等级：高风险 · 会删数据" {
		t.Fatalf("labelHarmReason(high) = %q", got)
	}
	if got := labelHarmReason("low", ""); got != "风险等级：低风险" {
		t.Fatalf("labelHarmReason(low, empty) = %q", got)
	}
	if got := labelHarmReason("weird", "x"); got != "x" {
		t.Fatalf("labelHarmReason(unknown tier) = %q, want the raw reason", got)
	}
}
