package edit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/diff"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

const maxEditableBytes = 1_000_000

type input struct {
	Path       *string `json:"path"`
	OldString  *string `json:"old_string"`
	NewString  *string `json:"new_string"`
	ReplaceAll bool    `json:"replace_all,omitempty"`
}

type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Edit"
}

func (Tool) Description() string {
	return "Replace exact text in a previously read file."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Path to the file to edit.",
			},
			"old_string": {
				Type:        tool.SchemaTypeString,
				Description: "Exact text to replace. Must be unique unless replace_all is true.",
			},
			"new_string": {
				Type:        tool.SchemaTypeString,
				Description: "Replacement text.",
			},
			"replace_all": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Replace all occurrences of old_string.",
				Default:     false,
			},
		},
		Required:             []string{"path", "old_string", "new_string"},
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
		return tool.Result{}, fmt.Errorf("parse edit input: %w", err)
	}
	if in.Path == nil || *in.Path == "" {
		return tool.Result{}, errors.New("path is required")
	}
	if in.OldString == nil || *in.OldString == "" {
		return tool.Result{}, errors.New("old_string is required")
	}
	if in.NewString == nil {
		return tool.Result{}, errors.New("new_string is required")
	}
	if *in.OldString == *in.NewString {
		return tool.Result{}, errors.New("old_string and new_string must differ")
	}
	target, err := toolpath.ResolveMutationTarget(*in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}
	if !target.Exists {
		return tool.Result{}, errors.New("file does not exist")
	}
	if !target.Within {
		return tool.Result{}, errors.New("path is outside the workspace")
	}
	// Trusting modes (judge/flight) skip the read-before-edit gate; the file is
	// still read below to locate old_string.
	if !tctx.TrustedWrites() {
		if err := toolpath.RequireFreshRead(target.Path, tctx); err != nil {
			return tool.Result{}, err
		}
	}
	info, err := os.Stat(target.Path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > maxEditableBytes {
		return tool.Result{}, errors.New("file is too large to edit")
	}
	data, err := os.ReadFile(target.Path)
	if err != nil {
		return tool.Result{}, fmt.Errorf("read file: %w", err)
	}
	text := string(data)
	count := strings.Count(text, *in.OldString)
	if count == 0 {
		return tool.Result{}, errors.New("old_string was not found")
	}
	if count > 1 && !in.ReplaceAll {
		return tool.Result{}, errors.New("old_string appears multiple times")
	}
	replaceCount := 1
	if in.ReplaceAll {
		replaceCount = -1
	}
	updated := strings.Replace(text, *in.OldString, *in.NewString, replaceCount)
	if err := os.WriteFile(target.Path, []byte(updated), 0o600); err != nil {
		return tool.Result{}, fmt.Errorf("write file: %w", err)
	}
	// Refresh the read record to the post-edit file, so consecutive edits to the
	// same file are not blocked as "read stale" by the read-before-write gate.
	if tctx != nil && tctx.ReadSet != nil {
		if info, statErr := os.Stat(target.Path); statErr == nil {
			tctx.ReadSet[target.Path] = tool.ReadFile{
				Path:     target.Path,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
				Complete: true,
			}
		}
	}
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "File edited."}},
		Output:  diff.Unified(text, updated, diff.DefaultOptions()),
	}, nil
}
