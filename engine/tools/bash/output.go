package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// BashOutputTool reads output produced by a background shell since the last read.
type BashOutputTool struct {
	mgr *Manager
}

// NewBashOutput returns a BashOutput tool sharing the given manager.
func NewBashOutput(mgr *Manager) tool.Tool {
	return BashOutputTool{mgr: mgr}
}

func (BashOutputTool) Name() string { return "BashOutput" }

func (BashOutputTool) Description() string {
	return "Read new output from a background shell started by Bash with run_in_background, and report whether it is still running."
}

func (BashOutputTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"bash_id": {
				Type:        tool.SchemaTypeString,
				Description: "The background shell id returned by Bash.",
			},
		},
		Required:             []string{"bash_id"},
		AdditionalProperties: false,
	}
}

func (BashOutputTool) IsConcurrencySafe() bool { return false }

type bashOutputInput struct {
	BashID string `json:"bash_id"`
}

func (t BashOutputTool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if t.mgr == nil {
		return tool.Result{}, errors.New("background shells are not available")
	}
	var in bashOutputInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse BashOutput input: %w", err)
	}
	if strings.TrimSpace(in.BashID) == "" {
		return tool.Result{}, errors.New("bash_id is required")
	}
	status, err := t.mgr.Output(in.BashID)
	if err != nil {
		return tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: err.Error()}}}, nil
	}
	return tool.Result{
		Metadata: map[string]any{"running": status.Running, "exit_code": status.ExitCode},
		Content:  []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: formatStatus(status)}},
	}, nil
}

// formatStatus renders a background shell's status and new output.
func formatStatus(s ShellStatus) string {
	var b strings.Builder
	if s.Running {
		fmt.Fprintf(&b, "status: running (shell %s)\n", s.ID)
	} else {
		fmt.Fprintf(&b, "status: exited (shell %s, exit_code %d)\n", s.ID, s.ExitCode)
	}
	b.WriteString("output:\n")
	b.WriteString(s.NewOutput)
	if s.NewOutput != "" && !strings.HasSuffix(s.NewOutput, "\n") {
		b.WriteString("\n")
	}
	if s.Truncated {
		b.WriteString("[output truncated]\n")
	}
	return b.String()
}
