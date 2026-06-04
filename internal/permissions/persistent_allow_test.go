package permissions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type recordingApprover struct {
	resp   ApprovalResponse
	called bool
}

func (r *recordingApprover) Prompt(_ context.Context, _ ApprovalRequest) (ApprovalResponse, error) {
	r.called = true
	return r.resp, nil
}

func writeAction() Action {
	return Action{
		Operation: OperationWrite,
		ToolName:  "Write",
		Resources: []Resource{{Type: ResourceFile, Path: "/repo/a.go"}},
	}
}

func writeRules(t *testing.T, workspace string, rules persistedRules) {
	t.Helper()
	dir := filepath.Join(workspace, permissionsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	data, err := json.Marshal(rules)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, permissionsFileName), data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFileAllowStorePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	key := "command\x00Bash\x00read_only\x00"

	first, err := OpenFileAllowStore(ws)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if first.Allowed(key) {
		t.Fatal("empty store should not allow")
	}
	if err := first.RememberPersistent(key); err != nil {
		t.Fatalf("remember persistent: %v", err)
	}

	// Simulate a new process by reopening from disk.
	second, err := OpenFileAllowStore(ws)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if !second.Allowed(key) {
		t.Fatal("persisted grant not loaded after reopen")
	}
}

func TestFileAllowStoreSessionGrantNotPersisted(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	key := "mutate\x00Write\x00/repo/a.go"

	first, _ := OpenFileAllowStore(ws)
	first.Remember(key)
	if !first.Allowed(key) {
		t.Fatal("session grant should be active in-process")
	}

	second, _ := OpenFileAllowStore(ws)
	if second.Allowed(key) {
		t.Fatal("session grant must not survive across processes")
	}
}

func TestFileAllowStoreDenyWinsOverAllow(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	key := "blocked"
	writeRules(t, ws, persistedRules{Version: 1, Allow: []string{key}, Deny: []string{key}})

	store, err := OpenFileAllowStore(ws)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !store.Denied(key) {
		t.Fatal("expected key on denylist")
	}
	if store.Allowed(key) {
		t.Fatal("deny must win over allow")
	}
	if err := store.RememberPersistent(key); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if store.Allowed(key) {
		t.Fatal("a denied key must never be promoted to allow")
	}
}

func TestFileAllowStoreCorruptFileErrors(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	dir := filepath.Join(ws, permissionsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, permissionsFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := OpenFileAllowStore(ws); err == nil {
		t.Fatal("want parse error for corrupt permissions file")
	}
}

func TestFileAllowStoreFilePermissions(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	store, _ := OpenFileAllowStore(ws)
	if err := store.RememberPersistent("k"); err != nil {
		t.Fatalf("remember: %v", err)
	}
	info, err := os.Stat(filepath.Join(ws, permissionsDir, permissionsFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		// Windows does not honor 0600 exactly; only assert on unix-like modes.
		if perm&0o077 != 0 && os.PathSeparator == '/' {
			t.Fatalf("permissions file mode = %o, want 0600", perm)
		}
	}
}

func TestAuthorizeDenylistBlocksBeforePrompt(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	action := writeAction()
	key := DefaultSessionKey(action)
	writeRules(t, ws, persistedRules{Version: 1, Deny: []string{key}})

	store, _ := OpenFileAllowStore(ws)
	approver := &recordingApprover{resp: ApprovalResponse{Effect: EffectAllow}}
	auth := InteractiveAuthorizer{Approver: approver, Store: store}

	decision := auth.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "rule"))
	if decision.FinalEffect != EffectDeny || decision.Reason != ReasonDenylisted {
		t.Fatalf("decision = %#v, want deny/denylisted", decision)
	}
	if approver.called {
		t.Fatal("a denylisted action must not reach the approver")
	}
}

func TestAuthorizeProjectScopePersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	action := writeAction()
	key := DefaultSessionKey(action)

	store, _ := OpenFileAllowStore(ws)
	approver := &recordingApprover{resp: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeProject}}
	auth := InteractiveAuthorizer{Approver: approver, Store: store}

	decision := auth.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "rule"))
	if decision.FinalEffect != EffectAllow {
		t.Fatalf("decision = %#v, want allow", decision)
	}

	reopened, _ := OpenFileAllowStore(ws)
	if !reopened.Allowed(key) {
		t.Fatal("project-scope grant was not persisted across reopen")
	}
}

func TestAuthorizeSessionScopeDoesNotPersist(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	action := writeAction()
	key := DefaultSessionKey(action)

	store, _ := OpenFileAllowStore(ws)
	approver := &recordingApprover{resp: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeSession}}
	auth := InteractiveAuthorizer{Approver: approver, Store: store}

	auth.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "rule"))

	reopened, _ := OpenFileAllowStore(ws)
	if reopened.Allowed(key) {
		t.Fatal("session-scope grant must not persist across reopen")
	}
}
