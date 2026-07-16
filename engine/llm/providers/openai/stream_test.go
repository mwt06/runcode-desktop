package openai

import (
	"encoding/json"
	"testing"

	"github.com/wt68/runcode/engine/llm"
)

func contentChunk(text string) chatChunk {
	return chatChunk{Choices: []chatChoice{{Delta: chatDelta{Content: text}}}}
}

func reasoningContentChunk(text string) chatChunk {
	return chatChunk{Choices: []chatChoice{{Delta: chatDelta{ReasoningContent: text}}}}
}

func reasoningFieldChunk(raw string) chatChunk {
	return chatChunk{Choices: []chatChoice{{Delta: chatDelta{Reasoning: json.RawMessage(raw)}}}}
}

// splitDeltas concatenates the answer text and thinking text from a stream.
func splitDeltas(events []llm.StreamEvent) (text, thinking string) {
	for _, e := range events {
		if e.Type == llm.StreamEventTypeContentBlockDelta && e.Delta != nil {
			text += e.Delta.Text
			thinking += e.Delta.Thinking
		}
	}
	return
}

func hasBlock(events []llm.StreamEvent, bt llm.ContentBlockType) bool {
	for _, e := range events {
		if e.Type == llm.StreamEventTypeContentBlockStart && e.Block != nil && e.Block.Type == bt {
			return true
		}
	}
	return false
}

func toolChunk(index int, id, name, args string) chatChunk {
	return chatChunk{Choices: []chatChoice{{Delta: chatDelta{ToolCalls: []chatToolCall{{
		Index:    index,
		ID:       id,
		Function: chatFunction{Name: name, Arguments: args},
	}}}}}}
}

func finishChunk(reason string) chatChunk {
	return chatChunk{Choices: []chatChoice{{FinishReason: &reason}}}
}

// drain collects all events a state emits for a sequence of chunks plus its
// terminal events, mirroring what the stream goroutine forwards.
func drain(st *streamState, chunks []chatChunk) []llm.StreamEvent {
	var events []llm.StreamEvent
	for _, c := range chunks {
		events = append(events, st.chunk(c)...)
	}
	return append(events, st.finishEvents()...)
}

func TestStreamStateText(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{contentChunk("Hel"), contentChunk("lo"), finishChunk("stop")})

	// message_start, block_start(0), delta, delta, block_stop(0), message_stop
	if events[0].Type != llm.StreamEventTypeMessageStart {
		t.Fatalf("first event = %v", events[0].Type)
	}
	if events[1].Type != llm.StreamEventTypeContentBlockStart || events[1].Index != 0 || events[1].Block.Type != llm.ContentBlockTypeText {
		t.Fatalf("block start = %#v", events[1])
	}
	var text string
	for _, e := range events {
		if e.Type == llm.StreamEventTypeContentBlockDelta && e.Delta != nil {
			text += e.Delta.Text
		}
	}
	if text != "Hello" {
		t.Fatalf("reassembled text = %q", text)
	}
	last := events[len(events)-1]
	if last.Type != llm.StreamEventTypeMessageStop || last.StopReason != llm.StopReasonEndTurn {
		t.Fatalf("terminal event = %#v", last)
	}
}

func TestStreamStateToolCall(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{
		toolChunk(0, "call_1", "Read", ""),
		toolChunk(0, "", "", `{"p":`),
		toolChunk(0, "", "", `1}`),
		finishChunk("tool_calls"),
	})

	var start *llm.StreamEvent
	var argJSON []byte
	for i := range events {
		e := events[i]
		if e.Type == llm.StreamEventTypeContentBlockStart && e.Block.Type == llm.ContentBlockTypeToolUse {
			start = &events[i]
		}
		if e.Type == llm.StreamEventTypeContentBlockDelta && e.Delta != nil {
			argJSON = append(argJSON, e.Delta.InputJSON...)
		}
	}
	if start == nil || start.Block.ID != "call_1" || start.Block.Name != "Read" {
		t.Fatalf("tool block start missing or wrong: %#v", start)
	}
	if string(argJSON) != `{"p":1}` {
		t.Fatalf("reassembled arguments = %q", string(argJSON))
	}
	last := events[len(events)-1]
	if last.StopReason != llm.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want tool_use", last.StopReason)
	}
}

func TestStreamStateTextThenToolGetDistinctIndexes(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	drainEvents := drain(st, []chatChunk{
		contentChunk("thinking out loud"),
		toolChunk(0, "call_1", "Glob", `{}`),
		finishChunk("tool_calls"),
	})
	indexes := map[int]llm.ContentBlockType{}
	for _, e := range drainEvents {
		if e.Type == llm.StreamEventTypeContentBlockStart {
			indexes[e.Index] = e.Block.Type
		}
	}
	if indexes[0] != llm.ContentBlockTypeText || indexes[1] != llm.ContentBlockTypeToolUse {
		t.Fatalf("expected text at 0 and tool at 1, got %#v", indexes)
	}
}

func TestStreamStateUsage(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{
		contentChunk("hi"),
		finishChunk("stop"),
		{Choices: []chatChoice{}, Usage: &chatUsage{PromptTokens: 11, CompletionTokens: 4}},
	})
	last := events[len(events)-1]
	if last.Usage == nil || last.Usage.InputTokens != 11 || last.Usage.OutputTokens != 4 {
		t.Fatalf("usage = %#v", last.Usage)
	}
}

func TestMapFinishReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		reason   string
		hasTools bool
		want     llm.StopReason
	}{
		{"stop", false, llm.StopReasonEndTurn},
		{"length", false, llm.StopReasonMaxTokens},
		{"tool_calls", false, llm.StopReasonToolUse},
		{"function_call", false, llm.StopReasonToolUse},
		{"content_filter", false, llm.StopReasonEndTurn},
		{"", true, llm.StopReasonToolUse},
		{"", false, llm.StopReasonEndTurn},
		{"weird", false, llm.StopReasonEndTurn},
		// Endpoints that report "stop" despite returning tool_calls.
		{"stop", true, llm.StopReasonToolUse},
		// Truncation wins even when tool calls were partially emitted.
		{"length", true, llm.StopReasonMaxTokens},
	}
	for _, c := range cases {
		if got := mapFinishReason(c.reason, c.hasTools); got != c.want {
			t.Errorf("mapFinishReason(%q,%v) = %q, want %q", c.reason, c.hasTools, got, c.want)
		}
	}
}

// TestStreamEndToEnd runs a canned chunk stream through the goroutine-backed
// stream and verifies the forwarded event order and terminal usage.
func TestStreamEndToEnd(t *testing.T) {
	t.Parallel()
	s := newStream(&mockSSE{chunks: []chatChunk{
		contentChunk("a"),
		contentChunk("b"),
		finishChunk("stop"),
		{Usage: &chatUsage{PromptTokens: 3, CompletionTokens: 2}},
	}})
	defer s.Close()

	var got []llm.StreamEvent
	for e := range s.Events() {
		got = append(got, e)
	}
	if err := s.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if got[0].Type != llm.StreamEventTypeMessageStart {
		t.Fatalf("first = %v", got[0].Type)
	}
	last := got[len(got)-1]
	if last.Type != llm.StreamEventTypeMessageStop || last.Usage == nil || last.Usage.InputTokens != 3 {
		t.Fatalf("terminal = %#v", last)
	}
}

func TestStreamReasoningContentChannel(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{
		reasoningContentChunk("let me think"),
		contentChunk("the answer"),
		finishChunk("stop"),
	})
	text, thinking := splitDeltas(events)
	if thinking != "let me think" || text != "the answer" {
		t.Fatalf("text=%q thinking=%q", text, thinking)
	}
	if !hasBlock(events, llm.ContentBlockTypeThinking) {
		t.Fatal("expected a thinking block to start")
	}
}

func TestStreamDualChannelReasoningNotDoubled(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	// Reproduces a gateway (e.g. Qwen on some OpenAI-compatible endpoints) that
	// mirrors each reasoning token into BOTH reasoning_content AND content as a
	// <think>…</think> span. The inline copy must be dropped so reasoning is not
	// counted twice, while content after </think> is the real answer.
	both := func(rc, ct string) chatChunk {
		return chatChunk{Choices: []chatChoice{{Delta: chatDelta{ReasoningContent: rc, Content: ct}}}}
	}
	events := drain(st, []chatChunk{
		both("the user", "<think>the user"),
		both(" wants", " wants"),
		contentChunk("</think>answer"),
		finishChunk("stop"),
	})
	text, thinking := splitDeltas(events)
	if thinking != "the user wants" {
		t.Fatalf("thinking=%q, want a single copy %q", thinking, "the user wants")
	}
	if text != "answer" {
		t.Fatalf("text=%q, want %q", text, "answer")
	}
}

func TestStreamReasoningFieldString(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{reasoningFieldChunk(`"deep thought"`), contentChunk("ok"), finishChunk("stop")})
	text, thinking := splitDeltas(events)
	if thinking != "deep thought" || text != "ok" {
		t.Fatalf("text=%q thinking=%q", text, thinking)
	}
}

func TestStreamReasoningFieldObject(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{reasoningFieldChunk(`{"content":"obj reason"}`), finishChunk("stop")})
	_, thinking := splitDeltas(events)
	if thinking != "obj reason" {
		t.Fatalf("thinking=%q", thinking)
	}
}

func TestStreamInlineThinkTags(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{contentChunk("<think>reasoning here</think>final answer"), finishChunk("stop")})
	text, thinking := splitDeltas(events)
	if thinking != "reasoning here" || text != "final answer" {
		t.Fatalf("text=%q thinking=%q", text, thinking)
	}
}

// A model may inline <think>…</think> with the tags split across streamed chunks;
// the splitter must not leak tag fragments into the answer.
func TestStreamInlineThinkSplitAcrossChunks(t *testing.T) {
	t.Parallel()
	st := newStreamState()
	events := drain(st, []chatChunk{
		contentChunk("pre <th"),
		contentChunk("ink>secret th"),
		contentChunk("oughts</thi"),
		contentChunk("nk> done"),
		finishChunk("stop"),
	})
	text, thinking := splitDeltas(events)
	if text != "pre  done" {
		t.Fatalf("text=%q, want %q", text, "pre  done")
	}
	if thinking != "secret thoughts" {
		t.Fatalf("thinking=%q, want %q", thinking, "secret thoughts")
	}
}

type mockSSE struct {
	chunks []chatChunk
	i      int
	err    error
}

func (m *mockSSE) Next() bool {
	if m.i >= len(m.chunks) {
		return false
	}
	m.i++
	return true
}

func (m *mockSSE) Current() chatChunk { return m.chunks[m.i-1] }
func (m *mockSSE) Err() error         { return m.err }
func (m *mockSSE) Close() error       { return nil }
