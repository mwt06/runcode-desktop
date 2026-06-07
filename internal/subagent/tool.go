package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/pkg/agent"
	"github.com/wt68/runcode/pkg/tool"
)

// ToolName is the model-facing name of the delegation tool.
const ToolName = "Task"

// Tool is the Task tool: it lets the main agent delegate a self-contained task to
// a named sub-agent. The tool resolves the requested sub-agent from the catalog
// and runs it via the Launcher, returning the sub-agent's final message as the
// tool result. It is the only seam through which sub-agents are spawned, and it is
// never granted to sub-agents themselves, so delegation stays one level deep.
type Tool struct {
	set      *agent.Set
	launcher *Launcher
}

// NewTool builds the Task tool over a loaded agent set and a launcher.
func NewTool(set *agent.Set, launcher *Launcher) *Tool {
	return &Tool{set: set, launcher: launcher}
}

func (t *Tool) Name() string { return ToolName }

func (t *Tool) Description() string {
	return "Delegate a focused, self-contained task to a sub-agent and receive its final report. Choose a sub-agent by its name (subagent_type) from the sub-agent catalog in the system prompt. The sub-agent runs autonomously with its own tools and instructions and cannot ask follow-up questions, so provide a complete, standalone prompt describing exactly what to do and what to report back. Use this to offload well-scoped investigations or multi-step work and keep your own context focused."
}

func (t *Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"description": {
				Type:        tool.SchemaTypeString,
				Description: "A short (3-7 word) description of the task, for display.",
			},
			"subagent_type": {
				Type:        tool.SchemaTypeString,
				Description: "The exact name of the sub-agent to run, as shown in the sub-agent catalog.",
			},
			"prompt": {
				Type:        tool.SchemaTypeString,
				Description: "The complete, standalone task for the sub-agent, including all context it needs and what it should report back.",
			},
		},
		Required: []string{"description", "subagent_type", "prompt"},
	}
}

// IsConcurrencySafe reports false: a sub-agent runs a full session with real tool
// side effects and may require interactive approval, so Task calls run serially in
// v1 to keep streaming output and approval prompts coherent.
func (t *Tool) IsConcurrencySafe() bool { return false }

type taskInput struct {
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
	Prompt       string `json:"prompt"`
}

// Run resolves and runs the requested sub-agent. Malformed input or an unknown
// sub-agent is returned as an is_error result (not a Go error) so the model can
// correct itself; a context cancellation is propagated as a Go error so the parent
// turn treats it as unrecoverable.
func (t *Tool) Run(ctx context.Context, input json.RawMessage, tctx *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in taskInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errorResult("invalid Task input: expected an object with \"subagent_type\" and \"prompt\" fields"), nil
	}
	subagentType := strings.TrimSpace(in.SubagentType)
	if subagentType == "" {
		return errorResult("Task requires a non-empty \"subagent_type\"; " + availableHint(t.set)), nil
	}
	taskPrompt := strings.TrimSpace(in.Prompt)
	if taskPrompt == "" {
		return errorResult("Task requires a non-empty \"prompt\" describing the task for the sub-agent"), nil
	}
	def, ok := t.set.Get(subagentType)
	if !ok {
		return errorResult(fmt.Sprintf("unknown sub-agent %q; %s", subagentType, availableHint(t.set))), nil
	}

	text, err := t.launcher.Launch(ctx, def, taskPrompt, tctx, out)
	if err != nil {
		// A cancellation/timeout is unrecoverable: surface it to the parent loop.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return tool.Result{}, err
		}
		return errorResult(fmt.Sprintf("sub-agent %q failed: %v", subagentType, err)), nil
	}
	if text == "" {
		text = fmt.Sprintf("Sub-agent %q completed but returned no output.", subagentType)
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}

// availableHint lists the sub-agent names the model can choose from.
func availableHint(set *agent.Set) string {
	names := set.Names()
	if len(names) == 0 {
		return "no sub-agents are available"
	}
	return "choose a subagent_type from: " + strings.Join(names, ", ")
}

func errorResult(msg string) tool.Result {
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}},
		IsError: true,
	}
}
