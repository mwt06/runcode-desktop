package write

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.ouc-online.com.cn/aibase/agentloop/diff"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

type input struct {
	Path    *string `json:"path"`
	Content *string `json:"content"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Write"
}

func (Tool) Description() string {
	return "Write complete file content, creating a new file or overwriting a previously read file."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Path to the file to write.",
			},
			"content": {
				Type:        tool.SchemaTypeString,
				Description: "Complete file content to write.",
			},
		},
		Required:             []string{"path", "content"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse write input: %w", err)
	}
	if in.Path == nil || *in.Path == "" {
		return tool.Result{}, errors.New("path is required")
	}
	if in.Content == nil {
		return tool.Result{}, errors.New("content is required")
	}
	target, err := toolpath.ResolveMutationTarget(*in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}
	if !target.Within {
		return tool.Result{}, errors.New("path is outside the workspace")
	}
	// Create any missing parent directories (mkdir -p). ResolveMutationTarget has
	// already confirmed the whole path stays within the workspace, so this only ever
	// creates folders under it.
	if !target.Exists {
		if err := os.MkdirAll(filepath.Dir(target.Path), 0o755); err != nil {
			return tool.Result{}, fmt.Errorf("create parent directories: %w", err)
		}
	}
	previous := ""
	if target.Exists {
		// Trusting modes (judge/flight) skip the read-before-overwrite gate.
		if !tctx.TrustedWrites() {
			if err := toolpath.RequireOverwritable(target.Path, tctx); err != nil {
				return tool.Result{}, err
			}
		}
		if data, readErr := os.ReadFile(target.Path); readErr == nil {
			previous = string(data)
		}
	}
	if err := os.WriteFile(target.Path, []byte(*in.Content), 0o600); err != nil {
		return tool.Result{}, fmt.Errorf("write file: %w", err)
	}
	// Record the file as freshly read: after writing, the agent holds the
	// authoritative content, so a subsequent overwrite (e.g. fixing the file it
	// just created) should not be blocked by the read-before-write gate.
	if tctx != nil {
		if info, statErr := os.Stat(target.Path); statErr == nil {
			if tctx.ReadSet == nil {
				tctx.ReadSet = map[string]tool.ReadFile{}
			}
			tctx.ReadSet[target.Path] = tool.ReadFile{
				Path:     target.Path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Complete: true,
			}
		}
	}
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "File written."}},
		Output:  diff.Unified(previous, *in.Content, diff.DefaultOptions()),
	}, nil
}
