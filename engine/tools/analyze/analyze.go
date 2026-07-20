// Package analyze implements the Analyze tool: when a thinking protocol is active
// the model records its structured analysis by passing every required step as
// {key, content}. The session gates other tools until the analysis is complete,
// which makes the thinking process enforced rather than advisory. The tool itself
// is side-effect-free — it validates and echoes the steps and emits a UI event.
package analyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Name is the stable tool identifier.
const Name = "Analyze"

type step struct {
	Key     string `json:"key"`
	Content string `json:"content"`
}

type input struct {
	Steps []step `json:"steps"`
}

type Tool struct{}

func New() tool.Tool { return Tool{} }

func (Tool) Name() string { return Name }

func (Tool) Description() string {
	return "Record your structured analysis for the active thinking protocol. Pass every required step as " +
		"{key, content} with concrete, task-specific content (not restating the hint). When a thinking protocol " +
		"is active you MUST call this first — other tools are blocked until the analysis is complete."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"steps": {
				Type:        tool.SchemaTypeArray,
				Description: "The filled analysis; include every required step key for the active protocol.",
				Items: &tool.Schema{
					Type: tool.SchemaTypeObject,
					Properties: map[string]tool.Schema{
						"key":     {Type: tool.SchemaTypeString, Description: "The step key."},
						"content": {Type: tool.SchemaTypeString, Description: "Concrete, task-specific content for this step."},
					},
					Required:             []string{"key", "content"},
					AdditionalProperties: false,
				},
			},
		},
		Required:             []string{"steps"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return false }

func (Tool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse analyze input: %w", err)
	}
	if len(in.Steps) == 0 {
		return tool.Result{}, errors.New("steps must not be empty")
	}
	var b strings.Builder
	b.WriteString("结构化分析已记录:\n")
	for _, s := range in.Steps {
		fmt.Fprintf(&b, "- %s:%s\n", s.Key, strings.TrimSpace(s.Content))
	}
	if out != nil {
		select {
		case out <- tool.Event{Type: tool.EventTypeProgress, ToolName: Name, Message: fmt.Sprintf("analysis: %d steps", len(in.Steps))}:
		default:
		}
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: strings.TrimRight(b.String(), "\n")}}}, nil
}
