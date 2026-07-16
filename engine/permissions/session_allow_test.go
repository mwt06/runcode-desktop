package permissions

import (
	"context"
	"testing"
)

func editAction(path string) Action {
	return Action{
		ToolName:  "Edit",
		Operation: OperationEdit,
		Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeWorkspace, Path: path}},
	}
}

func bashAction(category CommandCategory, capabilities ...CommandCapability) Action {
	return Action{
		ToolName:  "Bash",
		Operation: OperationExecute,
		Resources: []Resource{{Type: ResourceCommand, Scope: ResourceScopeWorkspace}},
		Metadata: map[string]any{
			MetadataCommandCategory:     string(category),
			MetadataCommandCapabilities: commandCapabilitiesStrings(capabilities),
		},
	}
}

func TestMemorySessionAllowStore(t *testing.T) {
	t.Parallel()

	store := NewMemorySessionAllowStore()
	if store.Allowed("k") {
		t.Fatal("empty store should not allow")
	}
	store.Remember("k")
	if !store.Allowed("k") {
		t.Fatal("store should allow remembered key")
	}
	store.Remember("")
	if store.Allowed("") {
		t.Fatal("empty key must never be allowed")
	}

	var nilStore *MemorySessionAllowStore
	if nilStore.Allowed("k") {
		t.Fatal("nil store should not allow")
	}
	nilStore.Remember("k") // must not panic
}

func TestDefaultSessionKey(t *testing.T) {
	t.Parallel()

	if key := DefaultSessionKey(editAction("/ws/a.go")); key == "" {
		t.Fatal("edit with path should be rememberable")
	}
	// Coarse on purpose: one grant covers every workspace file mutation — different
	// files and Write/Edit/Delete alike share a single key.
	if DefaultSessionKey(editAction("/ws/a.go")) != DefaultSessionKey(editAction("/ws/b.go")) {
		t.Fatal("different files should share a session key")
	}
	writeAct := Action{ToolName: "Write", Operation: OperationWrite, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeWorkspace, Path: "/ws/c.go"}}}
	deleteAct := Action{ToolName: "Delete", Operation: OperationDelete, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeWorkspace, Path: "/ws/d.go"}}}
	if DefaultSessionKey(editAction("/ws/a.go")) != DefaultSessionKey(writeAct) || DefaultSessionKey(writeAct) != DefaultSessionKey(deleteAct) {
		t.Fatal("Write, Edit, and Delete should share one mutation session key")
	}
	if key := DefaultSessionKey(editAction("")); key != "" {
		t.Fatalf("pathless mutation key = %q, want empty", key)
	}
	if key := DefaultSessionKey(bashAction(CommandCategoryTest, CommandCapabilityReadsWorkspace)); key == "" {
		t.Fatal("known bash category should be rememberable")
	}
	if DefaultSessionKey(bashAction(CommandCategoryTest)) == DefaultSessionKey(bashAction(CommandCategoryBuild)) {
		t.Fatal("different command categories must produce different keys")
	}
	// Unknown commands key by program: same program (different args) shares a grant;
	// different programs do not. With no command text, nothing is remembered.
	unknownCmd := func(cmd string) Action {
		return Action{
			ToolName:  "Bash",
			Operation: OperationExecute,
			Resources: []Resource{{Type: ResourceCommand, Scope: ResourceScopeWorkspace, Path: cmd}},
			Metadata:  map[string]any{MetadataCommandCategory: string(CommandCategoryUnknown)},
		}
	}
	if key := DefaultSessionKey(unknownCmd("python a.py")); key == "" {
		t.Fatal("unknown command with a program should be rememberable")
	}
	if DefaultSessionKey(unknownCmd("python a.py")) != DefaultSessionKey(unknownCmd("python b.py")) {
		t.Fatal("same program, different args should share a session key")
	}
	if DefaultSessionKey(unknownCmd("python a.py")) == DefaultSessionKey(unknownCmd("node x.js")) {
		t.Fatal("different programs must produce different keys")
	}
	if key := DefaultSessionKey(bashAction(CommandCategoryUnknown)); key != "" {
		t.Fatalf("unknown command with no text key = %q, want empty", key)
	}
}

func TestSessionKeyConstructorsAndParse(t *testing.T) {
	t.Parallel()

	// Empty inputs never produce a key (never remembered).
	if NetworkSessionKey("", "h") != "" || NetworkSessionKey("t", "") != "" {
		t.Fatal("network key with an empty part should be empty")
	}
	if MutateSessionKey("Write", "") != "" || CommandSessionKey("Bash", "", nil) != "" {
		t.Fatal("mutate/command key with an empty required part should be empty")
	}

	// Capabilities are order-independent.
	a := CommandSessionKey("Bash", "build", []string{"write", "network"})
	b := CommandSessionKey("Bash", "build", []string{"network", "write"})
	if a != b {
		t.Fatalf("command key depends on capability order: %q vs %q", a, b)
	}

	rule := ParseRule(NetworkSessionKey("WebFetch", "example.com"))
	if rule.Scope != ScopeNetwork || rule.Tool != "WebFetch" || rule.Target != "example.com" {
		t.Fatalf("ParseRule(network) = %#v", rule)
	}
	mrule := ParseRule(MutateSessionKey("Write", "/repo/a.go"))
	if mrule.Scope != ScopeMutate || mrule.Tool != "Write" || mrule.Target != "/repo/a.go" {
		t.Fatalf("ParseRule(mutate) = %#v", mrule)
	}
	// A key that did not come from a constructor still yields whatever is present.
	if got := ParseRule("garbage"); got.Scope != "garbage" || got.Tool != "" {
		t.Fatalf("ParseRule(garbage) = %#v, want scope-only", got)
	}
}

func TestInteractiveAuthorizerRemembersSessionGrant(t *testing.T) {
	t.Parallel()

	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeSession}}
	authorizer := InteractiveAuthorizer{Approver: approver, Store: NewMemorySessionAllowStore()}
	action := editAction("/ws/a.go")

	first := authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	if first.FinalEffect != EffectAllow || first.Reason != ReasonApprovalGranted {
		t.Fatalf("first decision = %#v, want granted allow", first)
	}
	if approver.calls != 1 {
		t.Fatalf("calls = %d, want 1 prompt", approver.calls)
	}

	second := authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	if second.FinalEffect != EffectAllow || second.Reason != ReasonSessionAllowed {
		t.Fatalf("second decision = %#v, want session-allowed without prompt", second)
	}
	if approver.calls != 1 {
		t.Fatalf("calls = %d, want no second prompt", approver.calls)
	}
}

func TestInteractiveAuthorizerOnceDoesNotRemember(t *testing.T) {
	t.Parallel()

	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeOnce}}
	authorizer := InteractiveAuthorizer{Approver: approver, Store: NewMemorySessionAllowStore()}
	action := editAction("/ws/a.go")

	authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	if approver.calls != 2 {
		t.Fatalf("calls = %d, want both prompts (once must not persist)", approver.calls)
	}
}

func TestInteractiveAuthorizerSessionScopeIgnoredWithoutStore(t *testing.T) {
	t.Parallel()

	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeSession}}
	authorizer := InteractiveAuthorizer{Approver: approver}
	action := editAction("/ws/a.go")

	authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	second := authorizer.Authorize(context.Background(), action, Ask(ReasonRequiresApproval, "test.ask"))
	if approver.calls != 2 {
		t.Fatalf("calls = %d, want both prompts when no store", approver.calls)
	}
	if second.FinalEffect != EffectAllow {
		t.Fatalf("second decision = %#v, want allow", second)
	}
}

func TestInteractiveAuthorizerForwardsTargets(t *testing.T) {
	t.Parallel()

	approver := &fakeApprover{response: ApprovalResponse{Effect: EffectAllow, Scope: ApprovalScopeOnce}}
	authorizer := InteractiveAuthorizer{Approver: approver}
	authorizer.Authorize(context.Background(), editAction("/ws/a.go"), Ask(ReasonRequiresApproval, "test.ask"))
	if len(approver.request.Targets) != 1 || approver.request.Targets[0] != "/ws/a.go" {
		t.Fatalf("targets = %#v, want resolved file path", approver.request.Targets)
	}
}
