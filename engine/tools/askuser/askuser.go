// Package askuser implements the AskUser tool: the model asks the user a question
// and the turn stops so the user can reply (picking a suggested option or typing a
// custom answer in the composer). It is side-effect-free — it validates and echoes
// the question; the interactive rendering lives in the UI, which reads the question
// and options from the tool call's input.
package askuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Name is the stable tool identifier. The executor halts the turn after it runs.
const Name = "AskUser"

type input struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

type Tool struct{}

func New() tool.Tool { return Tool{} }

func (Tool) Name() string { return Name }

func (Tool) Description() string {
	return "Ask the user a question and stop for their reply. Use this when you need a decision or missing " +
		"information you cannot determine yourself, instead of guessing. Give a clear question and optionally a few " +
		"suggested options; the user can pick one or type a custom answer. The turn stops after this call — the " +
		"user's next message is their answer, so do not answer on their behalf."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"question": {Type: tool.SchemaTypeString, Description: "The question to ask the user."},
			"options": {
				Type:        tool.SchemaTypeArray,
				Description: "Optional suggested answers shown as clickable choices; the user may still type a custom reply.",
				Items:       &tool.Schema{Type: tool.SchemaTypeString},
			},
		},
		Required:             []string{"question"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return false }

func (Tool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse askuser input: %w", err)
	}
	if strings.TrimSpace(in.Question) == "" {
		return tool.Result{}, errors.New("question is required")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "已向用户提出问题,等待回复:%s", strings.TrimSpace(in.Question))
	if len(in.Options) > 0 {
		fmt.Fprintf(&b, "(候选:%s)", strings.Join(in.Options, " / "))
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: b.String()}}}, nil
}
