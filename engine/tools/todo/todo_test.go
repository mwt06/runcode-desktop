package todo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/tool"
)

func run(t *testing.T, raw string, out chan tool.Event) (tool.Result, error) {
	t.Helper()
	return Tool{}.Run(context.Background(), json.RawMessage(raw), nil, out)
}

func TestTodoWriteValidList(t *testing.T) {
	t.Parallel()
	out := make(chan tool.Event, 1)
	res, err := run(t, `{"todos":[
		{"content":"build","status":"completed"},
		{"content":"test","status":"in_progress","activeForm":"Testing the build"},
		{"content":"ship","status":"pending"}
	]}`, out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "1/3 done") {
		t.Fatalf("summary missing: %q", text)
	}
	if !strings.Contains(text, "[x] build") || !strings.Contains(text, "[~] Testing the build") || !strings.Contains(text, "[ ] ship") {
		t.Fatalf("render = %q", text)
	}
	select {
	case ev := <-out:
		if ev.Type != tool.EventTypeProgress || !strings.Contains(ev.Message, "1/3") || !strings.Contains(ev.Message, "Testing the build") {
			t.Fatalf("event = %#v", ev)
		}
	default:
		t.Fatal("expected a progress event")
	}
}

func TestTodoWriteEventCarriesSnapshot(t *testing.T) {
	t.Parallel()
	out := make(chan tool.Event, 1)
	_, err := run(t, `{"todos":[
		{"content":"build","status":"completed"},
		{"content":"test","status":"in_progress","activeForm":"Testing the build"},
		{"content":"ship","status":"pending"}
	]}`, out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	ev := <-out
	snap, ok := ev.Data.(todoSnapshot)
	if !ok {
		t.Fatalf("event Data is not a todoSnapshot: %#v", ev.Data)
	}
	if snap.Done != 1 || snap.Total != 3 {
		t.Fatalf("snapshot counts = done %d total %d, want 1/3", snap.Done, snap.Total)
	}
	if len(snap.Items) != 3 {
		t.Fatalf("snapshot items = %d, want 3", len(snap.Items))
	}
	if snap.Items[0].Content != "build" || snap.Items[0].Status != statusCompleted {
		t.Fatalf("item0 = %#v", snap.Items[0])
	}
	if snap.Items[1].Status != statusInProgress || snap.Items[1].ActiveForm != "Testing the build" {
		t.Fatalf("item1 = %#v", snap.Items[1])
	}
	if snap.Items[2].Content != "ship" || snap.Items[2].Status != statusPending {
		t.Fatalf("item2 = %#v", snap.Items[2])
	}
}

func TestTodoWriteRejectsEmptyList(t *testing.T) {
	t.Parallel()
	if _, err := run(t, `{"todos":[]}`, nil); err == nil {
		t.Fatal("want error on empty list")
	}
}

func TestTodoWriteRejectsInvalidStatus(t *testing.T) {
	t.Parallel()
	if _, err := run(t, `{"todos":[{"content":"x","status":"doing"}]}`, nil); err == nil {
		t.Fatal("want error on invalid status")
	}
}

func TestTodoWriteRejectsMultipleInProgress(t *testing.T) {
	t.Parallel()
	if _, err := run(t, `{"todos":[{"content":"a","status":"in_progress"},{"content":"b","status":"in_progress"}]}`, nil); err == nil {
		t.Fatal("want error on more than one in_progress")
	}
}

func TestTodoWriteRejectsEmptyContent(t *testing.T) {
	t.Parallel()
	if _, err := run(t, `{"todos":[{"content":"   ","status":"pending"}]}`, nil); err == nil {
		t.Fatal("want error on empty content")
	}
}

func TestTodoWriteMetadata(t *testing.T) {
	t.Parallel()
	tl := Tool{}
	if tl.Name() != "TodoWrite" {
		t.Fatalf("name = %q", tl.Name())
	}
	if tl.IsConcurrencySafe() {
		t.Fatal("TodoWrite should not be concurrency-safe")
	}
	schema := tl.InputSchema()
	todos := schema.Properties["todos"]
	if todos.Type != tool.SchemaTypeArray || todos.Items == nil {
		t.Fatalf("todos should be an array with item schema: %#v", todos)
	}
}
