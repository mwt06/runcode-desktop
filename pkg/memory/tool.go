package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

// ToolName is the model-facing name of the memory-writing tool.
const ToolName = "Remember"

// Tool is the Remember tool: it lets the model save a durable fact to memory so it
// persists across sessions. It writes only runcode's own memory files at fixed,
// scope-derived paths (it never takes a path), so the permission layer treats it as
// side-effect-free management rather than an arbitrary file write.
type Tool struct {
	store        *Store
	defaultScope Scope
}

// NewTool builds the Remember tool over a Store. Facts default to project scope.
func NewTool(store *Store) *Tool {
	return &Tool{store: store, defaultScope: ScopeProject}
}

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Description() string {
	return "Save a durable fact to memory so it persists across sessions. Use it when you learn a stable, reusable preference, project convention, or recurring gotcha worth remembering next time — not for one-off details about the current task. Saved memories are shown back to you at the start of future sessions. By default a fact is saved to project memory (this workspace); pass scope \"user\" for a preference that should apply across all projects."
}

func (t *Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"fact": {
				Type:        tool.SchemaTypeString,
				Description: "The fact to remember, as a single concise sentence.",
			},
			"scope": {
				Type:        tool.SchemaTypeString,
				Description: "Where to save it: \"project\" (default, this workspace only) or \"user\" (applies across all projects).",
			},
		},
		Required: []string{"fact"},
	}
}

// IsConcurrencySafe reports false: Remember reads, dedups, and appends to a file,
// so it runs serially with sibling tool calls.
func (t *Tool) IsConcurrencySafe() bool { return false }

type rememberInput struct {
	Fact  string `json:"fact"`
	Scope string `json:"scope"`
}

// Run saves the fact. Malformed input, an unknown scope, or an unavailable scope is
// returned as an is_error result (not a Go error) so the model can correct itself.
func (t *Tool) Run(_ context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in rememberInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errorResult("invalid Remember input: expected an object with a \"fact\" field"), nil
	}
	if strings.TrimSpace(in.Fact) == "" {
		return errorResult("Remember requires a non-empty \"fact\""), nil
	}
	scope := t.defaultScope
	if s := strings.TrimSpace(in.Scope); s != "" {
		scope = Scope(strings.ToLower(s))
		if !scope.Valid() {
			return errorResult(fmt.Sprintf("invalid scope %q; use \"project\" or \"user\"", in.Scope)), nil
		}
	}

	res, err := t.store.Append(scope, in.Fact)
	if err != nil {
		if errors.Is(err, ErrScopeUnavailable) {
			return errorResult(fmt.Sprintf("cannot save to %s memory: it is not available in this environment", scope)), nil
		}
		if errors.Is(err, ErrEmptyFact) {
			return errorResult("Remember requires a non-empty \"fact\""), nil
		}
		return errorResult(fmt.Sprintf("failed to save memory: %v", err)), nil
	}
	if res.Duplicate {
		return textResult(fmt.Sprintf("Already in %s memory; not added again.", scope)), nil
	}
	return textResult(fmt.Sprintf("Saved to %s memory.", scope)), nil
}

func textResult(msg string) tool.Result {
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}}}
}

func errorResult(msg string) tool.Result {
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}},
		IsError: true,
	}
}
