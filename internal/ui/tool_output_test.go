package ui

import (
	"strings"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func TestRenderToolOutputLines(t *testing.T) {
	t.Parallel()

	progress := ToolProgress{
		ToolName: "Bash",
		Status:   ToolStatusCompleted,
		Output: []ToolOutputLine{
			{Stream: "stdout", Text: "hello"},
			{Stream: "stdout", Text: "world"},
		},
		OutputTotal: 2,
	}
	out := renderToolProgress(progress)
	for _, want := range []string{"hello", "world", "│"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render = %q, want %q", out, want)
		}
	}
}

func TestRenderToolOutputCollapseAndExpand(t *testing.T) {
	t.Parallel()

	lines := make([]ToolOutputLine, 8)
	for i := range lines {
		lines[i] = ToolOutputLine{Stream: "stdout", Text: "line"}
	}
	progress := ToolProgress{ToolName: "Bash", Status: ToolStatusCompleted, Output: lines, OutputTotal: 8}

	collapsed := renderToolProgress(progress)
	if !strings.Contains(collapsed, "+3 more lines") {
		t.Fatalf("collapsed = %q, want '+3 more lines' (8 lines, limit 5)", collapsed)
	}
	if !strings.Contains(collapsed, "ctrl+o to expand") {
		t.Fatalf("collapsed = %q, want expand hint", collapsed)
	}

	expanded := renderToolProgressGroup([]ChatMessage{{Role: RoleTool, Tool: &progress}}, true)
	if strings.Contains(expanded, "more lines") {
		t.Fatalf("expanded = %q, want all lines shown", expanded)
	}
}

func TestRenderToolOutputDiffStreams(t *testing.T) {
	t.Parallel()

	progress := ToolProgress{
		ToolName: "Edit",
		Status:   ToolStatusCompleted,
		Output: []ToolOutputLine{
			{Stream: "diff_context", Text: "  alpha"},
			{Stream: "diff_del", Text: "- beta"},
			{Stream: "diff_add", Text: "+ BETA"},
		},
		OutputTotal: 3,
	}
	out := renderToolProgress(progress)
	for _, want := range []string{"- beta", "+ BETA", "alpha"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render = %q, want %q", out, want)
		}
	}
}

func TestApplyToolEventSanitizesOutput(t *testing.T) {
	t.Parallel()

	model := New(&fakeService{})
	event := tool.Event{
		Type:        tool.EventTypeCompleted,
		ToolName:    "Bash",
		ToolUseID:   "toolu_b",
		Output:      []tool.OutputLine{{Stream: tool.OutputStreamStdout, Text: "be\x07ll"}},
		OutputTotal: 1,
	}
	updated, _ := model.Update(toolEventMsg{Event: event})
	model = updated.(Model)

	var progress *ToolProgress
	for i := range model.messages {
		for _, p := range model.messages[i].Tools {
			if p.ToolUseID == "toolu_b" {
				progress = p
			}
		}
	}
	if progress == nil {
		t.Fatal("tool progress not created")
	}
	if len(progress.Output) != 1 || progress.Output[0].Text != "bell" {
		t.Fatalf("output = %#v, want control char stripped to 'bell'", progress.Output)
	}
}

func TestToolOutputDoesNotRenderForEmptyProgress(t *testing.T) {
	t.Parallel()

	progress := ToolProgress{ToolName: "Read", Status: ToolStatusCompleted}
	out := renderToolProgress(progress)
	if strings.Contains(out, "│") {
		t.Fatalf("render = %q, want no output guide when there is no output", out)
	}
}
