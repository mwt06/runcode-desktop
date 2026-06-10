package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/pkg/agent"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

func TestLauncherReturnsFinalText(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("the sub-agent report"))
	l := NewLauncher(Options{Provider: provider, Model: "parent-model"})

	text, err := l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "be helpful"}, "do the task", toolCtx(t), nil)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if text != "the sub-agent report" {
		t.Fatalf("text = %q", text)
	}
	if provider.request.Model != "parent-model" {
		t.Fatalf("model = %q, want parent-model", provider.request.Model)
	}
	if len(provider.request.Messages) != 1 || llm.TextContent(provider.request.Messages[0]) != "do the task" {
		t.Fatalf("task prompt not delivered: %#v", provider.request.Messages)
	}
}

func TestLauncherAgentModelOverride(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("ok"))
	l := NewLauncher(Options{Provider: provider, Model: "parent-model"})

	if _, err := l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "p", Model: "override-model"}, "task", toolCtx(t), nil); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if provider.request.Model != "override-model" {
		t.Fatalf("model = %q, want override-model", provider.request.Model)
	}
}

func TestLauncherFiltersToolsByPolicy(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("ok"))
	l := NewLauncher(Options{
		Provider:      provider,
		Model:         "m",
		EligibleTools: []tool.Tool{fakeTool{name: "Read"}, fakeTool{name: "Write"}, fakeTool{name: "Bash"}},
	})

	if _, err := l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "p", Tools: []string{"Read", "Grep"}}, "task", toolCtx(t), nil); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got, want := specNames(provider.request.Tools), []string{"Read"}; !equalStrings(got, want) {
		t.Fatalf("filtered tools = %#v, want %#v", got, want)
	}
}

func TestLauncherInheritsAllToolsWhenUnset(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("ok"))
	l := NewLauncher(Options{
		Provider:      provider,
		Model:         "m",
		EligibleTools: []tool.Tool{fakeTool{name: "Read"}, fakeTool{name: "Write"}},
	})

	if _, err := l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "p"}, "task", toolCtx(t), nil); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if got, want := specNames(provider.request.Tools), []string{"Read", "Write"}; !equalStrings(got, want) {
		t.Fatalf("inherited tools = %#v, want %#v", got, want)
	}
}

func TestLauncherPersonaInSystemPrompt(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("ok"))
	l := NewLauncher(Options{Provider: provider, Model: "m", BasePrompt: prompt.AssemblerOpts{Agents: "PARENT CATALOG"}})

	def := agent.Agent{Name: "reviewer", Prompt: "Find bugs in the diff."}
	if _, err := l.Launch(context.Background(), def, "task", toolCtx(t), nil); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	system := systemText(provider.request)
	if !strings.Contains(system, "Find bugs in the diff.") {
		t.Fatalf("agent body missing from system prompt:\n%s", system)
	}
	if !strings.Contains(system, `"reviewer" sub-agent`) {
		t.Fatalf("persona framing missing from system prompt:\n%s", system)
	}
	if strings.Contains(system, "PARENT CATALOG") {
		t.Fatalf("sub-agent must not be told it can delegate; catalog leaked:\n%s", system)
	}
}

func TestTaskToolDelegatesToAgent(t *testing.T) {
	t.Parallel()

	provider := newFakeProvider(textEvents("delegated result"))
	set := agent.NewSet(BuiltinAgents())
	tt := NewTool(set, NewLauncher(Options{Provider: provider, Model: "m"}))

	result := runTask(t, tt, taskInput{Description: "d", SubagentType: GeneralPurposeName, Prompt: "investigate"})
	if result.IsError {
		t.Fatalf("unexpected error result: %#v", result)
	}
	if got := resultText(result); got != "delegated result" {
		t.Fatalf("result text = %q", got)
	}
}

func TestTaskToolUnknownAgent(t *testing.T) {
	t.Parallel()

	tt := NewTool(agent.NewSet(BuiltinAgents()), NewLauncher(Options{Provider: newFakeProvider(textEvents("x")), Model: "m"}))
	result := runTask(t, tt, taskInput{Description: "d", SubagentType: "nope", Prompt: "p"})
	if !result.IsError || !strings.Contains(resultText(result), "unknown sub-agent") {
		t.Fatalf("expected unknown-agent error, got %#v", result)
	}
}

func TestTaskToolValidatesInput(t *testing.T) {
	t.Parallel()

	tt := NewTool(agent.NewSet(BuiltinAgents()), NewLauncher(Options{Provider: newFakeProvider(textEvents("x")), Model: "m"}))

	missingPrompt := runTask(t, tt, taskInput{Description: "d", SubagentType: GeneralPurposeName})
	if !missingPrompt.IsError {
		t.Fatal("expected error for missing prompt")
	}
	missingType := runTask(t, tt, taskInput{Description: "d", Prompt: "p"})
	if !missingType.IsError {
		t.Fatal("expected error for missing subagent_type")
	}

	bad, err := tt.Run(context.Background(), json.RawMessage("not json"), toolCtx(t), nil)
	if err != nil {
		t.Fatalf("Run returned Go error for bad json: %v", err)
	}
	if !bad.IsError {
		t.Fatal("expected error result for malformed input")
	}
}

func TestTaskToolRejectsOversizedPrompt(t *testing.T) {
	t.Parallel()

	tt := NewTool(agent.NewSet(BuiltinAgents()), NewLauncher(Options{Provider: newFakeProvider(textEvents("x")), Model: "m"}))
	huge := strings.Repeat("a", maxPromptBytes+1)
	result := runTask(t, tt, taskInput{Description: "d", SubagentType: GeneralPurposeName, Prompt: huge})
	if !result.IsError || !strings.Contains(resultText(result), "too long") {
		t.Fatalf("expected oversized-prompt error, got %#v", result)
	}
}

func TestTaskToolConcurrencySafe(t *testing.T) {
	t.Parallel()

	tt := NewTool(agent.NewSet(BuiltinAgents()), nil)
	if !tt.IsConcurrencySafe() {
		t.Fatal("Task should be concurrency-safe so delegations can fan out")
	}
	if tt.Name() != ToolName {
		t.Fatalf("name = %q", tt.Name())
	}
}

func TestLauncherLimitsConcurrency(t *testing.T) {
	t.Parallel()

	const capacity = 2
	const launches = 5
	prov := newBlockingProvider()
	l := NewLauncher(Options{Provider: prov, Model: "m", MaxConcurrent: capacity})
	pctx := toolCtx(t)

	var wg sync.WaitGroup
	for i := 0; i < launches; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = l.Launch(context.Background(), agent.Agent{Name: "x", Prompt: "p"}, "task", pctx, nil)
		}()
	}

	// Let the launches saturate the cap, then confirm no extra slipped through.
	waitFor(t, func() bool { return prov.peak() >= capacity }, time.Second)
	time.Sleep(50 * time.Millisecond)
	if got := prov.peak(); got > capacity {
		t.Fatalf("peak concurrent sub-agents = %d, want <= %d", got, capacity)
	}

	close(prov.gate) // release all blocked streams
	wg.Wait()
	if got := prov.peak(); got > capacity {
		t.Fatalf("final peak concurrent sub-agents = %d, want <= %d", got, capacity)
	}
}

func TestLauncherAcquireRespectsContextCancel(t *testing.T) {
	t.Parallel()

	// One blocked launch saturates the single slot; a second launch must return
	// when its own context is cancelled instead of blocking forever on the
	// semaphore.
	prov := newBlockingProvider()
	l := NewLauncher(Options{Provider: prov, Model: "m", MaxConcurrent: 1})
	pctx := toolCtx(t)

	var first sync.WaitGroup
	first.Add(1)
	go func() {
		defer first.Done()
		_, _ = l.Launch(context.Background(), agent.Agent{Name: "a", Prompt: "p"}, "t", pctx, nil)
	}()
	waitFor(t, func() bool { return prov.peak() >= 1 }, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.Launch(ctx, agent.Agent{Name: "b", Prompt: "p"}, "t", pctx, nil)
		done <- err
	}()
	cancel() // the second launch is blocked on the saturated semaphore

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked launch returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled launch did not return; acquire ignored ctx")
	}

	close(prov.gate)
	first.Wait()
}

func TestToolOnlyHooksSuppressUserPromptSubmit(t *testing.T) {
	t.Parallel()

	rec := &recordingHooks{}
	h := toolOnlyHooks{inner: rec}
	h.Run(context.Background(), hooks.Input{Event: hooks.EventUserPromptSubmit, Prompt: "task"})
	h.Run(context.Background(), hooks.Input{Event: hooks.EventPreToolUse, ToolName: "Read"})
	h.Run(context.Background(), hooks.Input{Event: hooks.EventPostToolUse, ToolName: "Read"})

	if got, want := rec.events, []hooks.Event{hooks.EventPreToolUse, hooks.EventPostToolUse}; !equalEvents(got, want) {
		t.Fatalf("forwarded events = %#v, want %#v", got, want)
	}
}

func TestChildProgressMapping(t *testing.T) {
	t.Parallel()

	started, ok := childProgress(tool.Event{Type: tool.EventTypeStarted, ToolName: "Grep"})
	if !ok || started.Type != tool.EventTypeProgress || !strings.Contains(started.Message, "Grep") {
		t.Fatalf("started mapping = %#v ok=%v", started, ok)
	}
	if _, ok := childProgress(tool.Event{Type: tool.EventTypeCompleted, ToolName: "Grep"}); ok {
		t.Fatal("completed events should not surface")
	}
	// A surfaced progress line carries no tool name/id so the parent executor
	// attributes it to the Task call rather than spawning an orphan row.
	if started.ToolName != "" || started.ToolUseID != "" {
		t.Fatalf("progress should be unattributed: %#v", started)
	}
}

func TestEventBridgeForwardsAndDrains(t *testing.T) {
	t.Parallel()

	parent := make(chan tool.Event, 4)
	in, stop := startEventBridge(parent)
	in <- tool.Event{Type: tool.EventTypeStarted, ToolName: "Read"}
	in <- tool.Event{Type: tool.EventTypeCompleted, ToolName: "Read"} // dropped
	stop()

	got := drain(parent)
	if len(got) != 1 || got[0].Type != tool.EventTypeProgress {
		t.Fatalf("forwarded events = %#v", got)
	}
}

func TestEventBridgeNilParentDrains(t *testing.T) {
	t.Parallel()

	in, stop := startEventBridge(nil)
	in <- tool.Event{Type: tool.EventTypeStarted, ToolName: "Read"}
	stop() // must not deadlock with a nil parent
}

// --- helpers ---

func toolCtx(t *testing.T) *tool.Context {
	t.Helper()
	return &tool.Context{WorkingDirectory: t.TempDir(), ReadSet: map[string]tool.ReadFile{}}
}

func runTask(t *testing.T, tt *Tool, in taskInput) tool.Result {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	result, err := tt.Run(context.Background(), raw, toolCtx(t), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func resultText(r tool.Result) string {
	var parts []string
	for _, c := range r.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "")
}

func systemText(req llm.Request) string {
	var parts []string
	for _, b := range req.System {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

func specNames(specs []llm.ToolSpec) []string {
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.Name
	}
	return names
}

func drain(ch chan tool.Event) []tool.Event {
	var out []tool.Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// recordingHooks records the events it is asked to run, for hook-scoping tests.
type recordingHooks struct{ events []hooks.Event }

func (r *recordingHooks) Run(_ context.Context, in hooks.Input) hooks.Decision {
	r.events = append(r.events, in.Event)
	return hooks.Decision{}
}

func equalEvents(a, b []hooks.Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeTool is a no-op tool used only to verify tool-set filtering.
type fakeTool struct{ name string }

func (f fakeTool) Name() string             { return f.name }
func (f fakeTool) Description() string      { return f.name + " tool" }
func (f fakeTool) InputSchema() tool.Schema { return tool.Schema{Type: tool.SchemaTypeObject} }
func (f fakeTool) IsConcurrencySafe() bool  { return true }
func (f fakeTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	return tool.Result{}, nil
}

// fakeProvider records the last request and replays a fixed stream.
type fakeProvider struct {
	request llm.Request
	events  []llm.StreamEvent
}

func newFakeProvider(events []llm.StreamEvent) *fakeProvider {
	return &fakeProvider{events: events}
}

func (p *fakeProvider) Name() string                   { return "fake" }
func (p *fakeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }

func (p *fakeProvider) Stream(_ context.Context, req llm.Request) (llm.Stream, error) {
	p.request = req
	ch := make(chan llm.StreamEvent, len(p.events))
	for _, e := range p.events {
		ch <- e
	}
	close(ch)
	return &fakeStream{events: ch}, nil
}

// blockingProvider holds every Stream call open on a shared gate so a test can
// observe how many sub-agents run at once. It records the peak concurrency.
type blockingProvider struct {
	gate chan struct{}
	mu   sync.Mutex
	cur  int
	max  int
}

func newBlockingProvider() *blockingProvider { return &blockingProvider{gate: make(chan struct{})} }

func (p *blockingProvider) Name() string                   { return "blocking" }
func (p *blockingProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }

func (p *blockingProvider) Stream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	p.mu.Lock()
	p.cur++
	if p.cur > p.max {
		p.max = p.cur
	}
	p.mu.Unlock()

	select {
	case <-p.gate:
	case <-ctx.Done():
	}

	p.mu.Lock()
	p.cur--
	p.mu.Unlock()

	ev := textEvents("ok")
	ch := make(chan llm.StreamEvent, len(ev))
	for _, e := range ev {
		ch <- e
	}
	close(ch)
	return &fakeStream{events: ch}, nil
}

func (p *blockingProvider) peak() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.max
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

type fakeStream struct{ events chan llm.StreamEvent }

func (s *fakeStream) Events() <-chan llm.StreamEvent { return s.events }
func (s *fakeStream) Err() error                     { return nil }
func (s *fakeStream) Close() error                   { return nil }

func textEvents(text string) []llm.StreamEvent {
	return []llm.StreamEvent{
		{Type: llm.StreamEventTypeContentBlockStart, Index: 0, Block: &llm.ContentBlock{Type: llm.ContentBlockTypeText}},
		{Type: llm.StreamEventTypeContentBlockDelta, Index: 0, Delta: &llm.ContentDelta{Text: text}},
		{Type: llm.StreamEventTypeContentBlockStop, Index: 0},
		{Type: llm.StreamEventTypeMessageStop, StopReason: llm.StopReasonEndTurn, Usage: &llm.Usage{OutputTokens: 1}},
	}
}
