package anthropic

import (
	"encoding/json"
	"errors"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/wt68/runcode/pkg/llm"
)

func TestStreamConvertsTextEvents(t *testing.T) {
	t.Parallel()

	stream := newStream(&fakeSDKStream{events: []sdk.MessageStreamEventUnion{
		messageStartEvent(t),
		contentBlockStartEvent(t, 0, map[string]any{"type": "text", "text": ""}),
		contentBlockDeltaEvent(t, 0, map[string]any{"type": "text_delta", "text": "hello"}),
		contentBlockStopEvent(t, 0),
		messageDeltaEvent(t, "end_turn", 3, 5),
		messageStopEvent(t),
	}})

	events := collectEvents(t, stream)
	if len(events) != 5 {
		t.Fatalf("events len = %d, want 5: %#v", len(events), events)
	}
	if events[1].Block.Type != llm.ContentBlockTypeText {
		t.Fatalf("block type = %q, want text", events[1].Block.Type)
	}
	if events[2].Delta.Text != "hello" {
		t.Fatalf("delta text = %q, want hello", events[2].Delta.Text)
	}
	stop := events[4]
	if stop.StopReason != llm.StopReasonEndTurn {
		t.Fatalf("stop reason = %q, want end_turn", stop.StopReason)
	}
	if stop.Usage.InputTokens != 3 || stop.Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage: %#v", stop.Usage)
	}
}

func TestStreamConvertsToolUseEvents(t *testing.T) {
	t.Parallel()

	stream := newStream(&fakeSDKStream{events: []sdk.MessageStreamEventUnion{
		contentBlockStartEvent(t, 0, map[string]any{"type": "tool_use", "id": "toolu_123", "name": "Read", "input": map[string]any{}}),
		contentBlockDeltaEvent(t, 0, map[string]any{"type": "input_json_delta", "partial_json": `{"path"`}),
		contentBlockDeltaEvent(t, 0, map[string]any{"type": "input_json_delta", "partial_json": `:"a.txt"}`}),
		messageDeltaEvent(t, "tool_use", 1, 2),
		messageStopEvent(t),
	}})

	events := collectEvents(t, stream)
	if events[0].Block.Type != llm.ContentBlockTypeToolUse || events[0].Block.ID != "toolu_123" || events[0].Block.Name != "Read" {
		t.Fatalf("unexpected tool use block: %#v", events[0].Block)
	}
	if string(events[1].Delta.InputJSON) != `{"path"` || string(events[2].Delta.InputJSON) != `:"a.txt"}` {
		t.Fatalf("unexpected input json deltas: %q %q", events[1].Delta.InputJSON, events[2].Delta.InputJSON)
	}
	if events[3].StopReason != llm.StopReasonToolUse {
		t.Fatalf("stop reason = %q, want tool_use", events[3].StopReason)
	}
}

func TestStreamKeepsUnknownStopReason(t *testing.T) {
	t.Parallel()

	stream := newStream(&fakeSDKStream{events: []sdk.MessageStreamEventUnion{
		messageDeltaEvent(t, "pause_turn", 0, 0),
		messageStopEvent(t),
	}})
	events := collectEvents(t, stream)
	if events[0].StopReason != llm.StopReason("pause_turn") {
		t.Fatalf("stop reason = %q, want pause_turn", events[0].StopReason)
	}
}

func TestStreamErrAndClose(t *testing.T) {
	t.Parallel()

	expected := errors.New("stream failed")
	fake := &fakeSDKStream{err: expected}
	stream := newStream(fake)
	for range stream.Events() {
	}
	if !errors.Is(stream.Err(), expected) {
		t.Fatalf("stream err = %v, want %v", stream.Err(), expected)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	if !fake.closed {
		t.Fatal("expected sdk stream to be closed")
	}
}

func collectEvents(t *testing.T, stream llm.Stream) []llm.StreamEvent {
	t.Helper()
	var events []llm.StreamEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	return events
}

type fakeSDKStream struct {
	events []sdk.MessageStreamEventUnion
	index  int
	err    error
	closed bool
}

func (s *fakeSDKStream) Next() bool {
	if s.index >= len(s.events) {
		return false
	}
	s.index++
	return true
}

func (s *fakeSDKStream) Current() sdk.MessageStreamEventUnion {
	return s.events[s.index-1]
}

func (s *fakeSDKStream) Err() error {
	return s.err
}

func (s *fakeSDKStream) Close() error {
	s.closed = true
	return nil
}

func messageStartEvent(t *testing.T) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_123", "type": "message", "role": "assistant", "content": []any{}, "model": "claude-test", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}})
}

func contentBlockStartEvent(t *testing.T, index int, block map[string]any) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{"type": "content_block_start", "index": index, "content_block": block})
}

func contentBlockDeltaEvent(t *testing.T, index int, delta map[string]any) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{"type": "content_block_delta", "index": index, "delta": delta})
}

func contentBlockStopEvent(t *testing.T, index int) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{"type": "content_block_stop", "index": index})
}

func messageDeltaEvent(t *testing.T, stopReason string, inputTokens int, outputTokens int) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
			"server_tool_use":             map[string]any{},
		},
	})
}

func messageStopEvent(t *testing.T) sdk.MessageStreamEventUnion {
	t.Helper()
	return streamEvent(t, map[string]any{"type": "message_stop"})
}

func streamEvent(t *testing.T, payload map[string]any) sdk.MessageStreamEventUnion {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var event sdk.MessageStreamEventUnion
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	return event
}
