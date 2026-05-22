package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

var _ tool.Tool = (*mockTool)(nil)

type mockTool struct{}

func (mockTool) Name() string {
	return "mock"
}

func (mockTool) Description() string {
	return "Mock tool"
}

func (mockTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString},
		},
		Required: []string{"path"},
	}
}

func (mockTool) IsConcurrencySafe() bool {
	return true
}

func (mockTool) Run(_ context.Context, _ json.RawMessage, _ *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	out <- tool.Event{Type: tool.EventTypeProgress, ToolName: "mock", Message: "running"}
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "ok"}},
	}, nil
}

func TestToolContract(t *testing.T) {
	t.Parallel()

	out := make(chan tool.Event, 1)
	result, err := mockTool{}.Run(context.Background(), json.RawMessage(`{"path":"README.md"}`), &tool.Context{WorkingDirectory: "."}, out)
	if err != nil {
		t.Fatalf("run mock tool: %v", err)
	}
	if result.Content[0].Text != "ok" {
		t.Fatalf("unexpected result text: %q", result.Content[0].Text)
	}
	if event := <-out; event.Type != tool.EventTypeProgress {
		t.Fatalf("unexpected event type: %q", event.Type)
	}
}
