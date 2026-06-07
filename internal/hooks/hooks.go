// Package hooks runs user-configured commands at lifecycle events (before/after a
// tool runs, when a prompt is submitted) so the user can enforce policy, audit,
// or inject context without changing runcode. Hooks execute arbitrary commands,
// so they are honored only from user-level configuration.
package hooks

import (
	"context"
	"encoding/json"
)

// Event identifies a lifecycle point at which hooks fire.
type Event string

const (
	// EventPreToolUse fires after a tool call is authorized but before it runs. A
	// blocking hook prevents the tool from running.
	EventPreToolUse Event = "PreToolUse"
	// EventPostToolUse fires after a tool runs. It cannot un-run the tool; its
	// output is surfaced to the model as feedback.
	EventPostToolUse Event = "PostToolUse"
	// EventUserPromptSubmit fires when the user submits a prompt, before it is
	// sent to the model. A blocking hook rejects the prompt; otherwise the hook's
	// output is injected as additional context for the turn.
	EventUserPromptSubmit Event = "UserPromptSubmit"
)

// Hook is one configured hook.
type Hook struct {
	// Event is the lifecycle point this hook fires at.
	Event Event
	// Matcher selects which tool a tool-event hook applies to: "" or "*" matches
	// every tool, otherwise it must equal the tool name. Ignored for non-tool
	// events.
	Matcher string
	// Command is the executable and its arguments, run directly (no shell).
	Command []string
	// TimeoutMS bounds one run of the command (0 uses the default).
	TimeoutMS int
}

// Input is the event payload delivered to a hook command as JSON on stdin.
type Input struct {
	Event     Event           `json:"event"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
}

// Decision is the aggregated result of the hooks that fired for an event.
type Decision struct {
	// Block is true when a hook deliberately blocked (ran and exited non-zero).
	Block bool
	// Output is sanitized, bounded text: the blocking hook's output when Block is
	// true, otherwise the concatenated output of the hooks that produced any.
	Output string
}

// Runner runs the hooks that match an event and aggregates their decision.
type Runner interface {
	Run(ctx context.Context, in Input) Decision
}

// Noop is a Runner that does nothing — the default when no hooks are configured.
type Noop struct{}

// Run always allows with no output.
func (Noop) Run(context.Context, Input) Decision { return Decision{} }
