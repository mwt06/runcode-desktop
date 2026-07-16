// Package turn defines the transport-neutral types a session turn produces or
// consumes: the turn result, tool listings, and the edit-capture ports a host
// can implement. They are protocol types — every shell (CLI, desktop, server)
// serializes or maps them — so they live in a leaf package that depends only
// on the llm wire types, decoupled from the engine's internal ReAct loop.
package turn

import "github.com/wt68/runcode/engine/llm"

// ReasoningScenario names the task class the reasoning classifier assigns to a
// turn (e.g. troubleshooting, planning); the engine maps it to prompt guidance.
type ReasoningScenario string

// ReasoningClassification is the outcome of the per-turn reasoning
// classification call, attached to a Result when auto-classification runs.
type ReasoningClassification struct {
	Scenario   ReasoningScenario
	Confidence string
}

// Result is everything one user turn produced: the final assistant message,
// intermediate requests/messages for accounting, usage, and how the turn
// ended. Frontends map it to their own view or wire model.
type Result struct {
	FirstRequest            llm.Request
	FinalAssistant          llm.Message
	LastToolMessage         *llm.Message
	ToolResults             []llm.ContentBlock
	FinalStopReason         llm.StopReason
	FinalUsage              *llm.Usage
	Requests                []llm.Request
	AssistantMessages       []llm.Message
	ToolMessages            []llm.Message
	Usages                  []*llm.Usage
	Iterations              int
	ReasoningClassification *ReasoningClassification
	ClassificationRequest   *llm.Request
	ClassificationUsage     *llm.Usage
	// Stopped is true when the turn ended because the user denied a tool and asked
	// to stop, rather than the model finishing on its own. The conversation is
	// left well-formed (every tool_use is answered) so the next user message
	// continues normally.
	Stopped bool
}

// ToolDescriptor is a tool's name and description, for UI listing (e.g. an
// @-mention picker).
type ToolDescriptor struct {
	Name            string
	Description     string
	ConcurrencySafe bool
}

// EditRecorder captures the pre/post content of a Write/Edit mutation so a host
// (the desktop) can offer undo/review. The engine does no extra file IO itself:
// the executor only brackets the tool call and hands the recorder the mutation's
// workspace-relative path and tool-use id. Hosts that don't capture leave it nil.
type EditRecorder interface {
	// BeginEdit is called just before a Write/Edit runs. It returns a handle whose
	// Commit is called iff the tool succeeds, or nil to skip recording this edit.
	BeginEdit(relPath, toolUseID string) EditHandle
}

// EditHandle finishes one capture. Commit reads the post-edit state and returns the
// opaque payload to attach to the tool event's Data (nil to attach nothing). The
// engine treats the payload as opaque; the host defines its shape.
type EditHandle interface {
	Commit() (data any, err error)
}
