// Package todo implements the TodoWrite tool: the model records its current
// task list by passing the complete list each call (replacing the previous
// one). The tool is side-effect-free — it validates and echoes the list and
// emits a progress event for the UI; the list's continuity lives in the
// conversation history, so the tool keeps no state of its own.
package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/engine/tool"
)

const (
	statusPending    = "pending"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"
	maxTodos         = 100
)

type item struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

// todoSnapshot is the structured task list attached to the progress event's Data
// field so an in-process UI can render a live progress board. It rides alongside
// the human-readable Message (which older UIs still read); it is in-process only
// and never recorded to telemetry or transcripts.
type todoSnapshot struct {
	Items []item `json:"items"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

type input struct {
	Todos []item `json:"todos"`
}

type Tool struct{}

func New() tool.Tool { return Tool{} }

func (Tool) Name() string { return "TodoWrite" }

func (Tool) Description() string {
	return "Record the current task todo list. Pass the complete list each call; it replaces the previous one. " +
		"Each item has content, a status (pending, in_progress, completed), and an activeForm (present-continuous " +
		"label shown while in progress). Keep exactly one item in_progress at a time."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"todos": {
				Type:        tool.SchemaTypeArray,
				Description: "The complete todo list, replacing any previous list.",
				Items: &tool.Schema{
					Type: tool.SchemaTypeObject,
					Properties: map[string]tool.Schema{
						"content":    {Type: tool.SchemaTypeString, Description: "Imperative description of the task."},
						"status":     {Type: tool.SchemaTypeString, Description: "One of: pending, in_progress, completed."},
						"activeForm": {Type: tool.SchemaTypeString, Description: "Present-continuous form shown while in progress."},
					},
					Required:             []string{"content", "status"},
					AdditionalProperties: false,
				},
			},
		},
		Required:             []string{"todos"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return false }

func (Tool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse todo input: %w", err)
	}
	if len(in.Todos) == 0 {
		return tool.Result{}, errors.New("todos must not be empty")
	}
	if len(in.Todos) > maxTodos {
		return tool.Result{}, fmt.Errorf("too many todos (%d > %d)", len(in.Todos), maxTodos)
	}
	inProgress := 0
	for i, todo := range in.Todos {
		if strings.TrimSpace(todo.Content) == "" {
			return tool.Result{}, fmt.Errorf("todo %d: content is required", i+1)
		}
		switch todo.Status {
		case statusPending, statusCompleted:
		case statusInProgress:
			inProgress++
		default:
			return tool.Result{}, fmt.Errorf("todo %d: invalid status %q (want pending, in_progress, or completed)", i+1, todo.Status)
		}
	}
	if inProgress > 1 {
		return tool.Result{}, fmt.Errorf("only one todo may be in_progress at a time (got %d)", inProgress)
	}

	emitTodoEvent(out, in.Todos)
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: render(in.Todos)}}}, nil
}

func render(todos []item) string {
	done := countCompleted(todos)
	var b strings.Builder
	fmt.Fprintf(&b, "Todos (%d/%d done):\n", done, len(todos))
	for _, todo := range todos {
		marker := "[ ]"
		switch todo.Status {
		case statusCompleted:
			marker = "[x]"
		case statusInProgress:
			marker = "[~]"
		}
		label := todo.Content
		if todo.Status == statusInProgress && strings.TrimSpace(todo.ActiveForm) != "" {
			label = todo.ActiveForm
		}
		fmt.Fprintf(&b, "%s %s\n", marker, label)
	}
	return strings.TrimRight(b.String(), "\n")
}

func emitTodoEvent(out chan<- tool.Event, todos []item) {
	if out == nil {
		return
	}
	done := countCompleted(todos)
	message := fmt.Sprintf("todos %d/%d", done, len(todos))
	if current := currentLabel(todos); current != "" {
		message += ": " + current
	}
	select {
	case out <- tool.Event{
		Type:     tool.EventTypeProgress,
		ToolName: "TodoWrite",
		Message:  message,
		Data:     todoSnapshot{Items: todos, Done: done, Total: len(todos)},
	}:
	default:
	}
}

func countCompleted(todos []item) int {
	done := 0
	for _, todo := range todos {
		if todo.Status == statusCompleted {
			done++
		}
	}
	return done
}

func currentLabel(todos []item) string {
	for _, todo := range todos {
		if todo.Status != statusInProgress {
			continue
		}
		if strings.TrimSpace(todo.ActiveForm) != "" {
			return todo.ActiveForm
		}
		return todo.Content
	}
	return ""
}
