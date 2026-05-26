package write

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
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
	if target.Exists {
		if err := toolpath.RequireFreshRead(target.Path, tctx); err != nil {
			return tool.Result{}, err
		}
	}
	if err := os.WriteFile(target.Path, []byte(*in.Content), 0o600); err != nil {
		return tool.Result{}, fmt.Errorf("write file: %w", err)
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "File written."}}}, nil
}
