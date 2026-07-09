package repl

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

// EditRecorder captures the pre/post content of a Write/Edit mutation so a host
// (the desktop) can offer undo/review. The core does no file IO itself: the
// executor only brackets the tool call and hands the recorder the mutation's
// workspace-relative path and tool-use id. CLI leaves it nil (no capture).
type EditRecorder interface {
	// BeginEdit is called just before a Write/Edit runs. It returns a handle whose
	// Commit is called iff the tool succeeds, or nil to skip recording this edit.
	BeginEdit(relPath, toolUseID string) EditHandle
}

// EditHandle finishes one capture. Commit reads the post-edit state and returns the
// opaque payload to attach to the tool event's Data (nil to attach nothing). The
// core treats the payload as opaque; the desktop defines its shape (EditRecord).
type EditHandle interface {
	Commit() (data any, err error)
}

// isEditTool reports whether name is a file-mutating tool this layer snapshots.
func isEditTool(name string) bool { return name == "Write" || name == "Edit" }

// editMutationRelPath parses the workspace-relative target of a Write/Edit from its
// raw input, resolved against the workspace root. It returns ("", false) when the
// input has no usable path or the target escapes the workspace.
func editMutationRelPath(input []byte, tctx *tool.Context) (string, bool) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &in); err != nil || in.Path == "" {
		return "", false
	}
	root, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return "", false
	}
	abs, err := toolpath.Resolve(in.Path, tctx)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}
