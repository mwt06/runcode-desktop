package prompt

import (
	"fmt"

	"github.com/wt68/runcode/internal/prompt/sections"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

type AssemblerOpts struct {
	CWD               string
	Date              string
	Tools             []tool.Tool
	Skills            string
	Agents            string
	ProjectCtx        string
	Memory            string
	ShellInfo         string
	Reasoning         string
	PermissionMode    string
	PermissionContext string
	// AgentInstructions, when set, marks this session as a sub-agent and carries
	// its persona/system instructions. It is placed prominently near the top of
	// the prompt so the sub-agent's role dominates its behavior.
	AgentInstructions string
	// SupportsCacheControl marks static prompt sections with ephemeral cache
	// hints. It should reflect the provider's capability: enabling it for a
	// provider that ignores cache control (e.g. OpenAI) only adds no-op metadata.
	SupportsCacheControl bool
	// SystemPromptOverride, when set, replaces the framework identity prose (the
	// Intro and System sections) with the caller's text. Functional sections
	// (tools, skills, agents) and behavioral sections still follow, so tool use
	// and conventions keep working.
	SystemPromptOverride string
	// SystemPromptAppend, when set, is appended as a final static section after
	// the framework sections — the common "add extra instructions" case.
	SystemPromptAppend string
}

// section is one entry in the system-prompt table. static sections are identical
// across turns and carry cache hints; they are emitted before the cache
// boundary. dynamic sections (cwd, date, memory, …) follow it. Adding a section
// is a single table entry whose placement is explicit in its static flag.
type section struct {
	text   string
	static bool
}

func BuildSystemPrompt(opts AssemblerOpts) ([]llm.ContentBlock, error) {
	permissionContext := opts.PermissionContext
	if permissionContext == "" {
		permissionContext = sections.PermissionContext(opts.PermissionMode)
	}
	table := make([]section, 0, 14)
	if opts.SystemPromptOverride != "" {
		// Replace the framework identity prose; functional and behavioral sections
		// still follow so tools and conventions keep working.
		table = append(table, section{opts.SystemPromptOverride, true})
	} else {
		table = append(table,
			section{sections.Intro(), true},
			section{sections.System(), true},
		)
	}
	table = append(table,
		section{opts.AgentInstructions, true},
		section{sections.UsingTools(opts.Tools), true},
		section{opts.Skills, true},
		section{opts.Agents, true},
		section{sections.Actions(), true},
		section{sections.ToneAndStyle(), true},
		section{opts.SystemPromptAppend, true},
		section{opts.Reasoning, false},
		section{sections.EnvInfo(sections.EnvInfoInput{CWD: opts.CWD, Date: opts.Date, ShellInfo: opts.ShellInfo}), false},
		section{permissionContext, false},
		section{opts.Memory, false},
		section{opts.ProjectCtx, false},
	)

	staticCache := llm.CacheControlNone
	if opts.SupportsCacheControl {
		staticCache = llm.CacheControlEphemeral
	}

	// Two passes keep the static-before-boundary, dynamic-after invariant
	// regardless of table order, so a new entry only needs its static flag right.
	blocks := make([]llm.ContentBlock, 0, len(table)+1)
	for _, s := range table {
		if s.static && s.text != "" {
			blocks = append(blocks, textBlock(s.text, staticCache))
		}
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty system prompt: no static sections provided")
	}

	blocks = append(blocks, textBlock(DynamicBoundary, staticCache))
	for _, s := range table {
		if !s.static && s.text != "" {
			blocks = append(blocks, textBlock(s.text, llm.CacheControlNone))
		}
	}
	return blocks, nil
}

func textBlock(text string, cache llm.CacheControl) llm.ContentBlock {
	return llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: text, Cache: cache}
}
