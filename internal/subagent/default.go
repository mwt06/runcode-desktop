package subagent

import "github.com/wt68/runcode/pkg/agent"

// GeneralPurposeName is the built-in agent always available for delegation. It
// inherits every delegatable tool and is suited to open-ended investigation.
const GeneralPurposeName = "general-purpose"

// generalPurposePrompt is the persona body for the built-in general-purpose agent.
const generalPurposePrompt = `You are a general-purpose research and execution sub-agent. You handle open-ended, multi-step tasks delegated by the main agent — searching the codebase, reading files, tracing how something works, running read-only checks, and carrying out well-scoped changes.

Work autonomously and efficiently:
- Gather only the context the task needs; do not explore beyond it.
- Prefer search and read tools to build understanding before acting.
- If the task is ambiguous, make the most reasonable assumption and state it in your result rather than guessing silently.

Your final message is the entire result the main agent receives, so make it complete and specific: state what you found or did, cite concrete file paths and identifiers, and call out anything unresolved.`

// BuiltinAgents returns the agents shipped with runcode. Callers merge them after
// discovered definitions (see loadAgents) so a user or project agent of the same
// name shadows a builtin, which acts only as a fallback.
func BuiltinAgents() []agent.Agent {
	return []agent.Agent{
		{
			Name:        GeneralPurposeName,
			Description: "General-purpose agent for researching complex questions, searching the codebase, and executing multi-step tasks. Use when a task is open-ended or you are not confident you will find the right answer in a few steps.",
			Prompt:      generalPurposePrompt,
			Source:      agent.SourceBuiltin,
			// No Tools list: inherits every delegatable tool.
		},
	}
}
