package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/pkg/llm"
)

func TestResumeContinueAndPersistParsing(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir, "--resume", "sess_abc"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Resume != "sess_abc" || cfg.Continue || !cfg.PersistSession {
		t.Fatalf("cfg = %#v, want resume set, persist default on", cfg)
	}

	cfg, _, err = resolveChatConfig(configFlagsCmd(t, "--cwd", dir, "--continue", "--no-session"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !cfg.Continue || cfg.PersistSession {
		t.Fatalf("cfg = %#v, want continue set, persist disabled", cfg)
	}
}

func TestMaxContextTokensPrecedence(t *testing.T) {
	isolateConfigEnv(t)
	dir := t.TempDir()
	writeProjectConfig(t, dir, "max_context_tokens = 5000\n")

	cfg, _, err := resolveChatConfig(configFlagsCmd(t, "--cwd", dir))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.MaxContextTokens != 5000 {
		t.Fatalf("max context tokens = %d, want 5000 from file", cfg.MaxContextTokens)
	}

	cfg, _, err = resolveChatConfig(configFlagsCmd(t, "--cwd", dir, "--max-context-tokens", "8000"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.MaxContextTokens != 8000 {
		t.Fatalf("max context tokens = %d, want flag override", cfg.MaxContextTokens)
	}
}

func TestResumeMutuallyExclusiveFlags(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token")

	cmd := newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "m", "--resume", "sess_a", "--continue", "hi"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for --resume with --continue")
	}

	cmd = newChatCmd(&fakeChatRunner{})
	cmd.SetArgs([]string{"--model", "m", "--resume", "sess_a", "--session-id", "sess_b", "hi"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for --resume with --session-id")
	}
}

func TestResolveSessionIDContinuePicksLatest(t *testing.T) {
	dir := t.TempDir()
	// Seed two persisted sessions; sess_two is written last (most recent).
	for _, id := range []string{"sess_one", "sess_two"} {
		store, err := sessions.OpenJSONL(dir, id)
		if err != nil {
			t.Fatalf("open %s: %v", id, err)
		}
		if err := store.Append(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "x"}}}}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		store.Close(context.Background())
	}

	id, err := resolveSessionID(chatConfig{CWD: dir, Continue: true})
	if err != nil {
		t.Fatalf("resolveSessionID: %v", err)
	}
	if id != "sess_two" {
		t.Fatalf("continue session = %q, want sess_two (latest)", id)
	}
}

func TestResolveSessionIDContinueNoSessions(t *testing.T) {
	if _, err := resolveSessionID(chatConfig{CWD: t.TempDir(), Continue: true}); err == nil {
		t.Fatal("want error when no session to continue")
	}
}

func TestOpenSessionStorePersistsByDefault(t *testing.T) {
	dir := t.TempDir()
	store, err := openSessionStore(chatConfig{CWD: dir, PersistSession: true}, "sess_persist")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close(context.Background())
	if err := store.Append(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "hi"}}}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".runcode", "sessions", "sess_persist.jsonl")); err != nil {
		t.Fatalf("session file not created: %v", err)
	}
}

func TestOpenSessionStoreDisabled(t *testing.T) {
	dir := t.TempDir()
	store, err := openSessionStore(chatConfig{CWD: dir, PersistSession: false}, "sess_off")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Append(context.Background(), []llm.Message{{Role: llm.RoleUser}}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".runcode", "sessions")); !os.IsNotExist(err) {
		t.Fatalf("disabled store created files: %v", err)
	}
}
