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
}

func BuildSystemPrompt(opts AssemblerOpts) ([]llm.ContentBlock, error) {
	staticSections := []string{
		sections.Intro(),
		sections.System(),
		opts.AgentInstructions,
		sections.UsingTools(opts.Tools),
		opts.Skills,
		opts.Agents,
		sections.Actions(),
		sections.ToneAndStyle(),
	}
	permissionContext := opts.PermissionContext
	if permissionContext == "" {
		permissionContext = sections.PermissionContext(opts.PermissionMode)
	}
	dynamicSections := []string{
		opts.Reasoning,
		sections.EnvInfo(sections.EnvInfoInput{CWD: opts.CWD, Date: opts.Date, ShellInfo: opts.ShellInfo}),
		permissionContext,
		opts.Memory,
		opts.ProjectCtx,
	}

	staticCache := llm.CacheControlNone
	if opts.SupportsCacheControl {
		staticCache = llm.CacheControlEphemeral
	}

	blocks := make([]llm.ContentBlock, 0, len(staticSections)+len(dynamicSections)+1)
	for _, section := range staticSections {
		if section == "" {
			continue
		}
		blocks = append(blocks, textBlock(section, staticCache))
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty system prompt: no static sections provided")
	}

	blocks = append(blocks, textBlock(DynamicBoundary, staticCache))
	for _, section := range dynamicSections {
		if section == "" {
			continue
		}
		blocks = append(blocks, textBlock(section, llm.CacheControlNone))
	}
	return blocks, nil
}

func textBlock(text string, cache llm.CacheControl) llm.ContentBlock {
	return llm.ContentBlock{Type: llm.ContentBlockTypeText, Text: text, Cache: cache}
}
