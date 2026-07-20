package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/agent"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// seqProvider replays one scripted stream per Stream call and records every
// request, so a test can drive a child's multi-step ReAct loop (a tool call, then
// a final answer) and inspect what tools the child was actually offered.
type seqProvider struct {
	mu       sync.Mutex
	requests []llm.Request
	streams  [][]llm.StreamEvent
	n        int
}

func (p *seqProvider) Name() string                   { return "seq" }
func (p *seqProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }

func (p *seqProvider) Stream(_ context.Context, req llm.Request) (llm.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	var events []llm.StreamEvent
	if p.n < len(p.streams) {
		events = p.streams[p.n]
	}
	p.n++
	ch := make(chan llm.StreamEvent, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &fakeStream{events: ch}, nil
}

func subagentToolUseEvents(block llm.ContentBlock) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &block},
		{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
		{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonToolUse},
	}
}

// recordingTool records that it ran and returns a canned result, so a test can
// confirm the child sub-agent actually executed a tool (not just returned text).
type recordingTool struct {
	name string
	ran  *bool
	out  string
}

func (r recordingTool) Name() string             { return r.name }
func (r recordingTool) Description() string      { return r.name + " tool" }
func (r recordingTool) InputSchema() tool.Schema { return tool.Schema{Type: tool.SchemaTypeObject} }
func (r recordingTool) IsConcurrencySafe() bool  { return true }
func (r recordingTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	*r.ran = true
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: r.out}}}, nil
}

// TestDelegationRunsChildToolCallEndToEnd exercises the full delegation path the
// desktop "委派" runs: the Task tool resolves a built-in agent, the launcher spins
// up a child repl session with the agent's restricted tool set, the child runs its
// own ReAct loop (calls a tool, then reports), and the report flows back as the
// Task result. It also confirms the child's tool allowlist is enforced.
func TestDelegationRunsChildToolCallEndToEnd(t *testing.T) {
	t.Parallel()

	var readRan bool
	readTool := recordingTool{name: "Read", ran: &readRan, out: "file contents: func main() { ... }"}
	writeTool := fakeTool{name: "Write"} // not in code-explorer's allowlist → must be filtered out

	// Child script: turn 1 → call Read; turn 2 (after the tool result) → final report.
	readCall := llm.ContentBlock{Type: llm.ContentBlockTypeToolUse, ID: "tu_1", Name: "Read", Input: json.RawMessage(`{"path":"main.go"}`)}
	provider := &seqProvider{streams: [][]llm.StreamEvent{
		subagentToolUseEvents(readCall),
		textEvents("Investigation complete: the entry point is main() in main.go."),
	}}

	svc := permissions.NewService(permissions.Options{Mode: "flight"}) // allow-all so the child's tool call runs
	launcher := NewLauncher(Options{
		Provider:      provider,
		Model:         "m",
		EligibleTools: []tool.Tool{readTool, writeTool},
		Permissions:   svc,
	})
	tt := NewTool(agent.NewSet(BuiltinAgents()), launcher)

	result := runTask(t, tt, taskInput{Description: "explore", SubagentType: "code-explorer", Prompt: "find the entry point"})

	if result.IsError {
		t.Fatalf("delegation returned an error result: %s", resultText(result))
	}
	if got := resultText(result); !strings.Contains(got, "entry point is main()") {
		t.Fatalf("final report did not flow back: %q", got)
	}
	if !readRan {
		t.Fatal("child sub-agent never executed the Read tool — the ReAct loop did not run a tool call")
	}
	// code-explorer's allowlist is Read/Glob/Grep, so from {Read, Write} it is offered Read only.
	if len(provider.requests) < 2 {
		t.Fatalf("expected at least 2 model calls (tool call + final), got %d", len(provider.requests))
	}
	if offered := specNames(provider.requests[0].Tools); !equalStrings(offered, []string{"Read"}) {
		t.Fatalf("child offered tools = %#v, want [Read] (Write must be filtered out by the allowlist)", offered)
	}
}
