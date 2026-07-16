package prompt

import (
	"strings"
	"testing"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tools"
	"github.com/wt68/runcode/internal/prompt/sections"
)

func TestBuildSystemPromptReturnsTextBlocks(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp/runcode"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected blocks")
	}
	for _, block := range blocks {
		if block.Type != llm.ContentBlockTypeText {
			t.Fatalf("expected text block, got %q", block.Type)
		}
	}
}

func TestBuildSystemPromptIncludesModelIdentity(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp", Model: "deepseek-v4-flash-tx"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	var joined string
	for _, b := range blocks {
		joined += b.Text + "\n"
	}
	if !strings.Contains(joined, `"deepseek-v4-flash-tx"`) {
		t.Fatal("identity section must name the underlying model")
	}
	if !strings.Contains(joined, "Do NOT claim to be Claude") {
		t.Fatal("identity section must forbid impersonating other assistants")
	}
	// Identity must be a static (pre-boundary) section so it is cache-anchored.
	boundary := -1
	modelIdx := -1
	for i, b := range blocks {
		if b.Text == DynamicBoundary {
			boundary = i
		}
		if strings.Contains(b.Text, `"deepseek-v4-flash-tx"`) {
			modelIdx = i
		}
	}
	if boundary >= 0 && modelIdx >= 0 && modelIdx > boundary {
		t.Fatal("identity section must come before the dynamic boundary")
	}
}

func TestBuildSystemPromptNoIdentityWhenModelEmpty(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	for _, b := range blocks {
		if strings.Contains(b.Text, "Your underlying model is") {
			t.Fatal("no identity section expected when Model is unset")
		}
	}
}

func TestBuildSystemPromptBoundaryBetweenStaticAndDynamic(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp", ProjectCtx: "ctx", SupportsCacheControl: true})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	boundaryIdx := -1
	firstDynamicIdx := -1
	for i, block := range blocks {
		if block.Text == DynamicBoundary {
			boundaryIdx = i
		}
		if block.Cache == llm.CacheControlNone && firstDynamicIdx < 0 {
			firstDynamicIdx = i
		}
	}
	if boundaryIdx < 0 {
		t.Fatal("boundary block not found")
	}
	if firstDynamicIdx < 0 {
		t.Fatal("dynamic block not found")
	}
	if boundaryIdx >= firstDynamicIdx {
		t.Fatalf("boundary should come before dynamic blocks, boundaryIdx=%d dynamicIdx=%d", boundaryIdx, firstDynamicIdx)
	}
}

func TestBuildSystemPromptCachePolicy(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp", Date: "2026-05-19", ProjectCtx: "project context", SupportsCacheControl: true})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	seenBoundary := false
	for _, block := range blocks {
		if block.Text == DynamicBoundary {
			seenBoundary = true
			if block.Cache != llm.CacheControlEphemeral {
				t.Fatalf("boundary should be cacheable, got %q", block.Cache)
			}
			continue
		}
		if !seenBoundary && block.Cache != llm.CacheControlEphemeral {
			t.Fatalf("static block should be cacheable: %#v", block)
		}
		if seenBoundary && block.Cache != llm.CacheControlNone {
			t.Fatalf("dynamic block should not be cacheable: %#v", block)
		}
	}
}

func TestBuildSystemPromptNoCacheWhenUnsupported(t *testing.T) {
	t.Parallel()

	// With SupportsCacheControl false (e.g. an OpenAI-compatible provider), no
	// block should carry a cache hint.
	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp", ProjectCtx: "ctx"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	for _, block := range blocks {
		if block.Cache != llm.CacheControlNone {
			t.Fatalf("no block should be cacheable when the provider lacks cache support: %#v", block)
		}
	}
}

func TestBuildSystemPromptAppend(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{
		Tools:              tools.Builtins(),
		CWD:                "/tmp/runcode",
		SystemPromptAppend: "ALWAYS speak like a pirate.",
	})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	// The framework identity prose is still present, and the appended text is a
	// static section before the dynamic boundary.
	var sawIntro, sawAppendBeforeBoundary bool
	for _, b := range blocks {
		if b.Text == sections.Intro() {
			sawIntro = true
		}
		if b.Text == DynamicBoundary {
			break
		}
		if b.Text == "ALWAYS speak like a pirate." {
			sawAppendBeforeBoundary = true
		}
	}
	if !sawIntro {
		t.Fatal("append must not drop the framework Intro section")
	}
	if !sawAppendBeforeBoundary {
		t.Fatal("appended text should be a static section before the boundary")
	}
}

func TestBuildSystemPromptOverride(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{
		Tools:                tools.Builtins(),
		CWD:                  "/tmp/runcode",
		SystemPromptOverride: "You are a narrow SQL-only assistant.",
	})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	if blocks[0].Text != "You are a narrow SQL-only assistant." {
		t.Fatalf("block 0 = %q, want the override", blocks[0].Text)
	}
	// Override replaces the identity prose but keeps functional sections.
	var sawIntro, sawTools bool
	for _, b := range blocks {
		if b.Text == sections.Intro() {
			sawIntro = true
		}
		if b.Text == sections.UsingTools(tools.Builtins()) {
			sawTools = true
		}
	}
	if sawIntro {
		t.Fatal("override should replace the framework Intro section")
	}
	if !sawTools {
		t.Fatal("override must keep the functional tools section")
	}
}

func TestBuildSystemPromptStaticSectionOrder(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{Tools: tools.Builtins(), CWD: "/tmp/runcode"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	want := []string{
		sections.Intro(),
		sections.System(),
		sections.UsingTools(tools.Builtins()),
		sections.Actions(),
		sections.ToneAndStyle(),
		DynamicBoundary,
	}
	if len(blocks) < len(want) {
		t.Fatalf("expected at least %d blocks, got %d", len(want), len(blocks))
	}
	for i, text := range want {
		if blocks[i].Text != text {
			t.Fatalf("block %d = %q, want %q", i, blocks[i].Text, text)
		}
	}
}

func TestBuildSystemPromptKeepsEnvironmentDynamic(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{CWD: "/tmp/runcode", Date: "2026-05-19", ShellInfo: "bash"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, dynamicText := splitBlockText(t, blocks)
	for _, value := range []string{"/tmp/runcode", "2026-05-19", "bash"} {
		if strings.Contains(staticText, value) {
			t.Fatalf("static cacheable prompt contains dynamic value %q", value)
		}
		if !strings.Contains(dynamicText, value) {
			t.Fatalf("dynamic prompt missing value %q", value)
		}
	}
}

func TestBuildSystemPromptWithTools(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{Tools: tools.Builtins()})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, _ := splitBlockText(t, blocks)
	if !strings.Contains(staticText, "Tool: Read") {
		t.Fatal("expected static tool section mentioning Read")
	}
}

func TestBuildSystemPromptIncludesSkillsAsStatic(t *testing.T) {
	t.Parallel()

	const catalog = "Skill: deploy\nDescription: ship the service"
	blocks, err := BuildSystemPrompt(AssemblerOpts{Tools: tools.Builtins(), Skills: catalog})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, dynamicText := splitBlockText(t, blocks)
	if !strings.Contains(staticText, "Skill: deploy") {
		t.Fatalf("static prompt missing skill catalog: %q", staticText)
	}
	if strings.Contains(dynamicText, "Skill: deploy") {
		t.Fatalf("skill catalog should be static, found in dynamic: %q", dynamicText)
	}
	// The catalog sits after the tool section and before the actions section.
	toolIdx := strings.Index(staticText, "Tool: Read")
	skillIdx := strings.Index(staticText, "Skill: deploy")
	if toolIdx < 0 || skillIdx < 0 || toolIdx > skillIdx {
		t.Fatalf("skill catalog not placed after tools (tool=%d skill=%d)", toolIdx, skillIdx)
	}
}

func TestBuildSystemPromptWithoutSkills(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{Tools: tools.Builtins()})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}
	staticText, _ := splitBlockText(t, blocks)
	if strings.Contains(staticText, "Skill:") {
		t.Fatalf("expected no skill catalog, got %q", staticText)
	}
}

func TestBuildSystemPromptWithoutTools(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, _ := splitBlockText(t, blocks)
	if strings.Contains(staticText, "You have the following tools available") || strings.Contains(staticText, "Tool:") {
		t.Fatalf("expected no tool section, got %q", staticText)
	}
}

func TestBuildSystemPromptOptionalDynamicSections(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{Memory: "memory content", ProjectCtx: "project context"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	_, dynamicText := splitBlockText(t, blocks)
	if !strings.Contains(dynamicText, "memory content") {
		t.Fatal("expected memory in dynamic text")
	}
	if !strings.Contains(dynamicText, "project context") {
		t.Fatal("expected project context in dynamic text")
	}
}

func TestBuildSystemPromptIncludesPermissionContextAsDynamic(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{PermissionMode: "safe"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, dynamicText := splitBlockText(t, blocks)
	for _, value := range []string{"Permission mode: safe", "Write", "Edit", "Bash", "Read", "Glob", "Grep"} {
		if strings.Contains(staticText, value) {
			t.Fatalf("static cacheable prompt contains permission value %q", value)
		}
		if !strings.Contains(dynamicText, value) {
			t.Fatalf("dynamic prompt missing permission value %q", value)
		}
	}
}

func TestBuildSystemPromptIncludesInteractivePermissionContext(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{PermissionMode: "interactive"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	_, dynamicText := splitBlockText(t, blocks)
	for _, value := range []string{"Permission mode: interactive", "approval", "hard denied", "unknown", "privileged", "destructive", "outside-write", "complex shell-control"} {
		if !strings.Contains(dynamicText, value) {
			t.Fatalf("dynamic prompt missing permission value %q", value)
		}
	}
}

func TestBuildSystemPromptUsesExplicitPermissionContext(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{PermissionMode: "safe", PermissionContext: "custom permission context"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	_, dynamicText := splitBlockText(t, blocks)
	if !strings.Contains(dynamicText, "custom permission context") {
		t.Fatalf("dynamic prompt missing custom permission context: %q", dynamicText)
	}
	if strings.Contains(dynamicText, "Permission mode: safe") {
		t.Fatalf("dynamic prompt should not include generated permission context: %q", dynamicText)
	}
}

func TestBuildSystemPromptIncludesReasoningAsDynamic(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{Reasoning: "selected reasoning"})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	staticText, dynamicText := splitBlockText(t, blocks)
	if strings.Contains(staticText, "selected reasoning") {
		t.Fatal("expected reasoning outside static text")
	}
	if !strings.Contains(dynamicText, "selected reasoning") {
		t.Fatal("expected reasoning in dynamic text")
	}
}

func TestBuildSystemPromptDynamicSectionOrderWithPermissionContext(t *testing.T) {
	t.Parallel()

	blocks, err := BuildSystemPrompt(AssemblerOpts{
		Reasoning:         "selected reasoning",
		CWD:               "/tmp/runcode",
		PermissionContext: "permission context",
		Memory:            "memory content",
		ProjectCtx:        "project context",
	})
	if err != nil {
		t.Fatalf("BuildSystemPrompt: %v", err)
	}

	boundary := -1
	for i, block := range blocks {
		if block.Text == DynamicBoundary {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		t.Fatal("boundary block not found")
	}
	want := []string{"selected reasoning", "Current working directory: /tmp/runcode", "permission context", "memory content", "project context"}
	if len(blocks[boundary+1:]) < len(want) {
		t.Fatalf("expected at least %d dynamic blocks, got %d", len(want), len(blocks[boundary+1:]))
	}
	for i, text := range want {
		if blocks[boundary+1+i].Text != text {
			t.Fatalf("dynamic block %d = %q, want %q", i, blocks[boundary+1+i].Text, text)
		}
	}
}

func splitBlockText(t *testing.T, blocks []llm.ContentBlock) (string, string) {
	t.Helper()
	var static strings.Builder
	var dynamic strings.Builder
	seenBoundary := false
	for _, block := range blocks {
		if block.Text == DynamicBoundary {
			seenBoundary = true
			continue
		}
		if seenBoundary {
			dynamic.WriteString(block.Text)
			dynamic.WriteByte('\n')
			continue
		}
		static.WriteString(block.Text)
		static.WriteByte('\n')
	}
	if !seenBoundary {
		t.Fatal("boundary block not found")
	}
	return static.String(), dynamic.String()
}
