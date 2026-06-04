package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runPermissions executes the permissions command tree with a fresh command
// instance (cobra commands hold parse state, so each call needs its own) and
// returns the combined stdout/stderr plus any execution error.
func runPermissions(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("RUNCODE_CWD", "")
	cmd := permissionsCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestPermissionsListEmpty(t *testing.T) {
	dir := t.TempDir()
	out, err := runPermissions(t, "list", "--cwd", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No persisted permission rules") {
		t.Fatalf("out = %q, want empty notice", out)
	}
}

func TestPermissionsDenyAllowRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if _, err := runPermissions(t, "deny", "blocked.example", "--cwd", dir); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if _, err := runPermissions(t, "allow", "ok.example", "--cwd", dir); err != nil {
		t.Fatalf("allow: %v", err)
	}

	// Allowing a denied host is refused (deny wins).
	if _, err := runPermissions(t, "allow", "blocked.example", "--cwd", dir); err == nil {
		t.Fatal("allowing a denied host should error")
	}

	out, err := runPermissions(t, "list", "--cwd", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "[1] deny  network WebFetch blocked.example") {
		t.Fatalf("list = %q, want deny rule first", out)
	}
	if !strings.Contains(out, "[2] allow network WebFetch ok.example") {
		t.Fatalf("list = %q, want allow rule second", out)
	}

	// Remove the deny rule by its number, then confirm only the allow remains.
	if _, err := runPermissions(t, "remove", "1", "--cwd", dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, _ = runPermissions(t, "list", "--cwd", dir)
	if strings.Contains(out, "blocked.example") || !strings.Contains(out, "ok.example") {
		t.Fatalf("list after remove = %q, want only the allow rule", out)
	}
}

func TestPermissionsRemoveRejectsBadNumber(t *testing.T) {
	dir := t.TempDir()
	if _, err := runPermissions(t, "deny", "h.example", "--cwd", dir); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if _, err := runPermissions(t, "remove", "5", "--cwd", dir); err == nil {
		t.Fatal("remove out-of-range number should error")
	}
	if _, err := runPermissions(t, "remove", "abc", "--cwd", dir); err == nil {
		t.Fatal("remove non-numeric should error")
	}
}

func TestPermissionsClearScoped(t *testing.T) {
	dir := t.TempDir()
	mustRun := func(args ...string) {
		if _, err := runPermissions(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	mustRun("deny", "d.example", "--cwd", dir)
	mustRun("allow", "a.example", "--cwd", dir)

	// Clearing only the deny list keeps the allow rule.
	out, err := runPermissions(t, "clear", "--deny", "--cwd", dir)
	if err != nil {
		t.Fatalf("clear --deny: %v", err)
	}
	if !strings.Contains(out, "cleared 1 rule") {
		t.Fatalf("clear out = %q", out)
	}
	out, _ = runPermissions(t, "list", "--cwd", dir)
	if strings.Contains(out, "d.example") || !strings.Contains(out, "a.example") {
		t.Fatalf("list after clear --deny = %q, want only allow rule", out)
	}

	// Clearing all empties the file.
	mustRun("clear", "--cwd", dir)
	out, _ = runPermissions(t, "list", "--cwd", dir)
	if !strings.Contains(out, "No persisted permission rules") {
		t.Fatalf("list after clear all = %q", out)
	}
}

// TestPermissionsListAndRemoveTUIWrittenRules verifies the management CLI renders
// and removes the mutation/command grains the interactive "allow for project"
// prompt writes (keys that embed NUL separators), not just network hosts.
func TestPermissionsListAndRemoveTUIWrittenRules(t *testing.T) {
	dir := t.TempDir()
	runcodeDir := filepath.Join(dir, ".runcode")
	if err := os.MkdirAll(runcodeDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Real keys use NUL separators; Go string literals embed them directly.
	content := "{\"version\":1," +
		"\"allow\":[\"mutate\\u0000Write\\u0000/repo/src/main.go\"]," +
		"\"deny\":[\"command\\u0000Bash\\u0000package-management\\u0000network,write\"]}"
	if err := os.WriteFile(filepath.Join(runcodeDir, "permissions.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write rules: %v", err)
	}

	out, err := runPermissions(t, "list", "--cwd", dir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "deny  command Bash package-management network,write") {
		t.Fatalf("list = %q, want readable command deny rule", out)
	}
	if !strings.Contains(out, "allow mutate Write /repo/src/main.go") {
		t.Fatalf("list = %q, want readable mutate allow rule", out)
	}

	// Remove the mutate allow (rule [2]: deny is listed first).
	if _, err := runPermissions(t, "remove", "2", "--cwd", dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, _ = runPermissions(t, "list", "--cwd", dir)
	if strings.Contains(out, "main.go") {
		t.Fatalf("list after remove = %q, want mutate rule gone", out)
	}
	if !strings.Contains(out, "package-management") {
		t.Fatalf("list after remove = %q, want command deny still present", out)
	}
}
