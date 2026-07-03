// Package delete implements the Delete tool, which removes a workspace file or
// directory. By default the target is moved to the operating system's recycle
// bin / trash (recoverable); permanent removal is opt-in. It exists so the model
// has a safe, approval-gated way to delete — shell rm/del are blocked.
package delete

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

type input struct {
	Path      *string `json:"path"`
	Permanent bool    `json:"permanent,omitempty"`
}

// Tool removes a workspace file or directory, defaulting to the recycle bin.
type Tool struct{}

func New() tool.Tool {
	return Tool{}
}

func (Tool) Name() string {
	return "Delete"
}

func (Tool) Description() string {
	return "Delete a file or directory in the workspace. By default it is moved to the system recycle bin (recoverable); set permanent=true to delete it irreversibly. Prefer this over shell rm/del, which are blocked."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {
				Type:        tool.SchemaTypeString,
				Description: "Path to the file or directory to delete.",
			},
			"permanent": {
				Type:        tool.SchemaTypeBoolean,
				Description: "Delete irreversibly instead of moving to the recycle bin.",
				Default:     false,
			},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool {
	return false
}

// lockedHint adds a "file may be open" note when the delete failed because the
// file is in use (a common case on Windows, e.g. a .pptx open in PowerPoint).
func lockedHint(err error) string {
	msg := strings.ToLower(err.Error())
	for _, sign := range []string{"being used", "access is denied", "permission denied", "sharing violation", "in use", "locked"} {
		if strings.Contains(msg, sign) {
			return " The file may be open in another program (e.g. PowerPoint); close it and retry."
		}
	}
	return ""
}

func (Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse delete input: %w", err)
	}
	if in.Path == nil || *in.Path == "" {
		return tool.Result{}, errors.New("path is required")
	}
	target, err := toolpath.ResolveMutationTarget(*in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}
	if !target.Within {
		return tool.Result{}, errors.New("path is outside the workspace")
	}
	if !target.Exists {
		return tool.Result{}, fmt.Errorf("file does not exist: %s", target.Path)
	}

	var summary string
	if in.Permanent {
		if err := os.RemoveAll(target.Path); err != nil {
			return tool.Result{
				IsError: true,
				Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("Could not delete %s: %v.%s", target.Path, err, lockedHint(err))}},
			}, nil
		}
		summary = "Permanently deleted: " + target.Path
	} else {
		if err := moveToTrash(target.Path); err != nil {
			// Surface as a tool error (not a Go error) with an actionable hint, so the
			// model can retry with permanent=true rather than abandon the task.
			return tool.Result{
				IsError: true,
				Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("Could not move %s to the recycle bin: %v.%s Or retry with permanent=true.", target.Path, err, lockedHint(err))}},
			}, nil
		}
		summary = "Moved to recycle bin: " + target.Path
	}

	// The path is gone, so drop any stale fresh-read record for it.
	if tctx != nil && tctx.ReadSet != nil {
		delete(tctx.ReadSet, target.Path)
	}
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: summary}},
	}, nil
}
