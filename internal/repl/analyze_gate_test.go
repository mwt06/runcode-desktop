package repl

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
)

func toolResultText(block llm.ContentBlock) string {
	var b strings.Builder
	for _, c := range block.Content {
		if c.Type == llm.ContentBlockTypeText {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func analyzeStepsInput(t *testing.T, proto ReasoningProtocol, fill func(ReasoningStep) string) json.RawMessage {
	t.Helper()
	var steps []map[string]string
	for _, s := range proto.Steps {
		steps = append(steps, map[string]string{"key": s.Key, "content": fill(s)})
	}
	return rawInput(t, map[string]any{"steps": steps})
}

func TestAnalyzeGateBlocksOtherToolsUntilComplete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sample.txt"), "alpha\n")
	session := newTestSession(t, SessionOptions{
		Provider:    newFakeProvider(textEvents("x"), nil),
		Tools:       tools.Builtins(),
		ToolContext: &tool.Context{WorkingDirectory: dir},
	})
	proto, ok := protocolFor(ReasoningScenarioTroubleshooting)
	if !ok {
		t.Fatal("troubleshooting protocol missing")
	}
	gate := &analyzeGate{proto: proto}
	session.analyzeGate = gate

	// A non-Analyze tool is blocked while the gate is unsatisfied.
	read := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "t1", Name: "Read", Input: rawInput(t, map[string]any{"path": "sample.txt"})}
	results, _, err := session.executeGatedToolUses(context.Background(), []llm.ContentBlock{read}, "turn1", gate)
	if err != nil {
		t.Fatalf("gate read: %v", err)
	}
	if !results[0].IsError || !strings.Contains(toolResultText(results[0]), "Analyze") {
		t.Fatalf("Read should be gated, got %q (err=%v)", toolResultText(results[0]), results[0].IsError)
	}
	if gate.satisfied {
		t.Fatal("gate satisfied without a complete analysis")
	}

	// An incomplete Analyze errors and does not satisfy the gate.
	incomplete := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "t2", Name: "Analyze", Input: analyzeStepsInput(t, proto, func(s ReasoningStep) string {
		if s.Key == proto.Steps[0].Key {
			return "只填了第一步"
		}
		return ""
	})}
	results, _, err = session.executeGatedToolUses(context.Background(), []llm.ContentBlock{incomplete}, "turn1", gate)
	if err != nil {
		t.Fatalf("gate incomplete analyze: %v", err)
	}
	if !results[0].IsError || gate.satisfied {
		t.Fatalf("incomplete analysis should error and not satisfy, got err=%v satisfied=%v", results[0].IsError, gate.satisfied)
	}

	// A complete Analyze runs and satisfies the gate.
	complete := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "t3", Name: "Analyze", Input: analyzeStepsInput(t, proto, func(ReasoningStep) string { return "具体内容" })}
	results, _, err = session.executeGatedToolUses(context.Background(), []llm.ContentBlock{complete}, "turn1", gate)
	if err != nil {
		t.Fatalf("gate complete analyze: %v", err)
	}
	if results[0].IsError || !gate.satisfied {
		t.Fatalf("complete analysis should satisfy the gate, got err=%v satisfied=%v", results[0].IsError, gate.satisfied)
	}
}
