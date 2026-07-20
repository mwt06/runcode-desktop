package bash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// KillShellTool terminates a background shell started by Bash.
type KillShellTool struct {
	mgr *Manager
}

// NewKillShell returns a KillShell tool sharing the given manager.
func NewKillShell(mgr *Manager) tool.Tool {
	return KillShellTool{mgr: mgr}
}

func (KillShellTool) Name() string { return "KillShell" }

func (KillShellTool) Description() string {
	return "Terminate a background shell started by Bash with run_in_background."
}

func (KillShellTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"shell_id": {
				Type:        tool.SchemaTypeString,
				Description: "The background shell id returned by Bash.",
			},
		},
		Required:             []string{"shell_id"},
		AdditionalProperties: false,
	}
}

func (KillShellTool) IsConcurrencySafe() bool { return false }

type killShellInput struct {
	ShellID string `json:"shell_id"`
}

func (t KillShellTool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if t.mgr == nil {
		return tool.Result{}, errors.New("background shells are not available")
	}
	var in killShellInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse KillShell input: %w", err)
	}
	if strings.TrimSpace(in.ShellID) == "" {
		return tool.Result{}, errors.New("shell_id is required")
	}
	status, err := t.mgr.Kill(in.ShellID)
	if err != nil {
		return tool.Result{IsError: true, Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: err.Error()}}}, nil
	}
	text := fmt.Sprintf("Killed background shell %s (exit_code %d).", status.ID, status.ExitCode)
	if status.NewOutput != "" {
		text += "\nfinal output:\n" + status.NewOutput
	}
	return tool.Result{
		Metadata: map[string]any{"exit_code": status.ExitCode},
		Content:  []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}},
	}, nil
}
