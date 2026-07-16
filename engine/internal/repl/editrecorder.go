package repl

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/toolpath"
	"github.com/wt68/runcode/engine/turn"
)

// EditRecorder / EditHandle alias the public turn protocol ports (see
// turn.EditRecorder for the capture contract).
type EditRecorder = turn.EditRecorder

// EditHandle aliases the public turn protocol type.
type EditHandle = turn.EditHandle

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
