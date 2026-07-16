package main

import (
	"testing"

	"github.com/wt68/runcode/engine/hooks"
	"github.com/wt68/runcode/engine/settings"
)

func TestHooksFromConfig(t *testing.T) {
	t.Parallel()
	got, err := hooksFromConfig([]settings.HookConfig{
		{Event: "PreToolUse", Matcher: "Bash", Command: []string{"audit.sh", "--strict"}, TimeoutMS: 1000},
		{Event: "UserPromptSubmit", Command: []string{"context.sh"}},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("hooks = %d, want 2", len(got))
	}
	if got[0].Event != hooks.EventPreToolUse || got[0].Matcher != "Bash" || got[0].TimeoutMS != 1000 {
		t.Fatalf("hook[0] = %#v", got[0])
	}
	if got[1].Event != hooks.EventUserPromptSubmit {
		t.Fatalf("hook[1] event = %q", got[1].Event)
	}
}

func TestHooksFromConfigRejectsBadEventAndCommand(t *testing.T) {
	t.Parallel()
	if _, err := hooksFromConfig([]settings.HookConfig{{Event: "Nope", Command: []string{"x"}}}); err == nil {
		t.Fatal("expected error for an unknown event")
	}
	if _, err := hooksFromConfig([]settings.HookConfig{{Event: "PreToolUse"}}); err == nil {
		t.Fatal("expected error for a missing command")
	}
	if _, err := hooksFromConfig([]settings.HookConfig{{Event: "PreToolUse", Command: []string{"  "}}}); err == nil {
		t.Fatal("expected error for a blank command")
	}
}

func TestResolveChatConfigStripsProjectHooks(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	// A project file must not be able to register hooks (which run commands).
	writeProjectConfig(t, dir, "[[hooks]]\nevent = \"PreToolUse\"\ncommand = [\"evil.sh\"]\n")

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(cfg.Hooks) != 0 {
		t.Fatalf("project hooks must be stripped, got %#v", cfg.Hooks)
	}
}
