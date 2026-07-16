package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNoopRecorder(t *testing.T) {
	t.Parallel()

	recorder := Noop()
	recorder.Record(context.Background(), NewEvent(EventTurnStart))
	if err := recorder.Close(context.Background()); err != nil {
		t.Fatalf("close noop: %v", err)
	}
}

func TestJSONLRecorderWritesEvents(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	recorder := NewJSONL(&buf)
	recorder.Record(context.Background(), Event{
		Time:    time.Date(2026, 5, 25, 1, 2, 3, 0, time.UTC),
		Name:    EventTurnStart,
		TraceID: "trace_1",
		TurnID:  "turn_1",
		Attributes: Attrs{
			string(AttrModel): "claude-test",
		},
	})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1: %q", len(lines), buf.String())
	}
	var event Event
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if event.Name != EventTurnStart || event.TraceID != "trace_1" || event.Attributes[string(AttrModel)] != "claude-test" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestJSONLRecorderWritesOneEventPerLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	recorder := NewJSONL(&buf)
	recorder.Record(context.Background(), Event{Name: EventTurnStart})
	recorder.Record(context.Background(), Event{Name: EventTurnEnd})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2: %q", len(lines), buf.String())
	}
}

func TestAsyncRecorderFlushes(t *testing.T) {
	t.Parallel()

	memory := NewMemory()
	recorder := NewAsync(memory, AsyncOptions{BufferSize: 2})
	recorder.Record(context.Background(), Event{Name: EventTurnStart})
	recorder.Record(context.Background(), Event{Name: EventTurnEnd})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := recorder.Close(ctx); err != nil {
		t.Fatalf("close async: %v", err)
	}
	events := memory.Events()
	if len(events) != 2 || events[0].Name != EventTurnStart || events[1].Name != EventTurnEnd {
		t.Fatalf("unexpected events: %#v", events)
	}
}

func TestAsyncRecorderDoesNotBlockWhenFull(t *testing.T) {
	t.Parallel()

	recorder := NewAsync(blockingRecorder{}, AsyncOptions{BufferSize: 1})
	recorder.Record(context.Background(), Event{Name: EventTurnStart})
	recorder.Record(context.Background(), Event{Name: EventTurnEnd})
	if recorder.Dropped() == 0 {
		t.Fatal("expected dropped event when buffer is full")
	}
}

func TestMemoryRecorderReturnsCopy(t *testing.T) {
	t.Parallel()

	recorder := NewMemory()
	recorder.Record(context.Background(), Event{Name: EventTurnStart})
	events := recorder.Events()
	events[0].Name = EventTurnEnd
	if got := recorder.Events()[0].Name; got != EventTurnStart {
		t.Fatalf("memory recorder exposed internal slice, got %q", got)
	}
}

func TestIDsAreNonEmpty(t *testing.T) {
	t.Parallel()

	for name, id := range map[string]string{
		"trace":   NewTraceID(),
		"turn":    NewTurnID(),
		"request": NewRequestID(),
	} {
		if id == "" {
			t.Fatalf("%s id is empty", name)
		}
	}
}

type blockingRecorder struct{}

func (blockingRecorder) Record(context.Context, Event) {
	select {}
}

func (blockingRecorder) Close(context.Context) error {
	return nil
}
