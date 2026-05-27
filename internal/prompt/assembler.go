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
	ProjectCtx        string
	Memory            string
	ShellInfo         string
	Reasoning         string
	PermissionMode    string
	PermissionContext string
}

func BuildSystemPrompt(opts AssemblerOpts) ([]llm.ContentBlock, error) {
	staticSections := []string{
		sections.Intro(),
		sections.System(),
		sections.UsingTools(opts.Tools),
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

	blocks := make([]llm.ContentBlock, 0, len(staticSections)+len(dynamicSections)+1)
	for _, section := range staticSections {
		if section == "" {
			continue
		}
		blocks = append(blocks, textBlock(section, llm.CacheControlEphemeral))
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("empty system prompt: no static sections provided")
	}

	blocks = append(blocks, textBlock(DynamicBoundary, llm.CacheControlEphemeral))
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
