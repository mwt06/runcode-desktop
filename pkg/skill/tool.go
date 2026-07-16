package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wt68/runcode/pkg/tool"
)

// ToolName is the model-facing name of the skill-loading tool.
const ToolName = "Skill"

// Tool adapts a loaded Set to the runcode tool.Tool interface. It is the
// progressive-disclosure seam: the model reads the catalog in the system prompt
// and calls this tool to load a specific skill's full instructions. The tool only
// returns in-memory text — it launches nothing and touches no files — so the
// permission layer classifies it as side-effect-free management.
type Tool struct {
	mu  sync.RWMutex
	set *Set
}

// NewTool builds the Skill tool over a loaded set.
func NewTool(set *Set) *Tool { return &Tool{set: set} }

// SetSet swaps the tool's skill set at runtime (e.g. after the user edits skills
// in the desktop), so a Skill call resolves against the latest skills without a
// session restart. It is safe to call concurrently with Run.
func (t *Tool) SetSet(set *Set) {
	t.mu.Lock()
	t.set = set
	t.mu.Unlock()
}

func (t *Tool) currentSet() *Set {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.set
}

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Description() string {
	return "Load a skill's full instructions by name. Skills are reusable workflows listed in the skill catalog in the system prompt; when one is relevant, call this tool with its name to load the detailed instructions, then follow them."
}

func (t *Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"name": {
				Type:        tool.SchemaTypeString,
				Description: "The exact name of the skill to load, as shown in the skill catalog.",
			},
		},
		Required: []string{"name"},
	}
}

// IsConcurrencySafe reports true: loading a skill is a read of in-memory data
// with no side effects, so it may run alongside other concurrency-safe tools.
func (t *Tool) IsConcurrencySafe() bool { return true }

type skillInput struct {
	Name string `json:"name"`
}

// Run returns the named skill's body. An unknown or malformed request is returned
// as an is_error result (not a Go error) so the model can correct itself and pick
// a valid skill from the catalog.
func (t *Tool) Run(_ context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in skillInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errorResult("invalid Skill input: expected an object with a \"name\" field"), nil
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return errorResult("Skill requires a non-empty \"name\""), nil
	}
	sk, ok := t.currentSet().Get(name)
	if !ok {
		return errorResult(fmt.Sprintf("unknown skill %q; choose a name from the skill catalog", name)), nil
	}
	text := sk.Body
	if strings.TrimSpace(text) == "" {
		text = fmt.Sprintf("Skill %q has no instructions.", name)
	}
	if sk.Truncated {
		text += "\n\n[skill instructions truncated]"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: locationHeader(sk) + text}}}, nil
}

// locationHeader prefixes a loaded skill's instructions with the directory it
// lives in. A skill is a directory, and its body routinely points at bundled
// resources by a path relative to that directory ("run scripts/gen.py", "see
// references/palette.md"). The body alone gives the model no anchor for those
// paths, leaving it to search the filesystem for files whose location is already
// known here. Naming the directory once, on load, removes that hunt.
//
// The path stays out of the catalog on purpose: the catalog is injected into
// every prompt, and progressive disclosure keeps it to name + description.
//
// Path is always set by the loader; it is empty only for a hand-built Skill, in
// which case there is no location to disclose.
func locationHeader(sk Skill) string {
	if strings.TrimSpace(sk.Path) == "" {
		return ""
	}
	return fmt.Sprintf("Skill: %s\nSkill directory: %s\n"+
		"Files this skill refers to by a relative path (scripts, references, templates) are in that "+
		"directory — resolve them against it and read them directly; do not search the workspace for them.\n\n",
		sk.Name, filepath.Dir(sk.Path))
}

func errorResult(msg string) tool.Result {
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}},
		IsError: true,
	}
}
