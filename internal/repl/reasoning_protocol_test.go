package repl

import (
	"strings"
	"testing"
)

func TestReasoningProtocolParseAndRender(t *testing.T) {
	t.Parallel()

	proto, ok := protocolFor(ReasoningScenarioArchitecture)
	if !ok {
		t.Fatal("architecture protocol missing")
	}

	// A valid JSON reply (with surrounding prose) fills every step.
	reply := "分析如下:\n{\"goal\":\"清晰边界\",\"constraints\":\"兼容\",\"options\":\"A/B\",\"tradeoffs\":\"取舍\",\"recommendation\":\"选 B\",\"risks\":\"迁移\"}\n"
	filled, err := proto.parseStructuredAnalysis(reply)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if filled["recommendation"] != "选 B" {
		t.Fatalf("recommendation = %q, want 选 B", filled["recommendation"])
	}
	rendered := proto.renderAnalysis(filled)
	if !strings.Contains(rendered, "推荐:选 B") || !strings.Contains(rendered, proto.Method) {
		t.Fatalf("render missing content/method: %q", rendered)
	}

	// Unparseable or empty analyses error so the caller can fall back.
	for _, bad := range []string{"no json here", "{}", `{"goal":""}`} {
		if _, err := proto.parseStructuredAnalysis(bad); err == nil {
			t.Fatalf("parseStructuredAnalysis(%q) = nil err, want error", bad)
		}
	}

	// Every scenario has a protocol with steps and a valid exec mode.
	for _, scenario := range []ReasoningScenario{
		ReasoningScenarioTroubleshooting, ReasoningScenarioProposal, ReasoningScenarioArchitecture,
		ReasoningScenarioProject, ReasoningScenarioIncident, ReasoningScenarioGeneral,
	} {
		p, ok := protocolFor(scenario)
		if !ok || len(p.Steps) == 0 {
			t.Fatalf("scenario %q has no protocol/steps", scenario)
		}
		if p.Mode != ReasoningExecPreTurn && p.Mode != ReasoningExecInTurn {
			t.Fatalf("scenario %q has invalid mode %q", scenario, p.Mode)
		}
	}
}
