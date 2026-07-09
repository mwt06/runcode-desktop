// Package preview implements the open_preview tool: the model asks the desktop to
// open a workspace file in its preview panel. It validates the path is inside the
// workspace and exists, then emits a structured event the desktop UI acts on. It is
// registered only in the desktop (via engine.Options.ExtraTools), never the CLI.
package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

// previewData is the structured payload the desktop UI reads off the event to open
// a preview tab. In-process UI only.
type previewData struct {
	Path string `json:"path"`
}

type input struct {
	Path string `json:"path"`
}

// Tool is the open_preview tool.
type Tool struct{}

// New returns the open_preview tool.
func New() tool.Tool { return Tool{} }

func (Tool) Name() string { return "open_preview" }

func (Tool) Description() string {
	return "Open a workspace file in the user's desktop preview panel. Call this after you " +
		"produce a document or a website/H5 (e.g. an .html, .md, or image) so the user sees it " +
		"immediately. The path is a workspace-relative file path."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString, Description: "Workspace-relative path of the file to preview."},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return true }

func (Tool) Run(_ context.Context, raw json.RawMessage, tctx *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse open_preview input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.Result{}, errors.New("path is required")
	}
	ws, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	abs, err := toolpath.Resolve(in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}
	within, err := toolpath.IsWithinResolved(ws, abs)
	if err != nil || !within {
		return tool.Result{}, fmt.Errorf("path is outside the workspace: %s", in.Path)
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		return tool.Result{}, fmt.Errorf("file not found: %s", in.Path)
	}
	rel, err := filepath.Rel(ws, abs)
	if err != nil {
		rel = in.Path
	}
	rel = filepath.ToSlash(rel)
	if out != nil {
		select {
		case out <- tool.Event{Type: tool.EventTypeProgress, ToolName: "open_preview", Message: "预览 " + rel, Data: previewData{Path: rel}}:
		default:
		}
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "已在桌面打开预览：" + rel}}}, nil
}
