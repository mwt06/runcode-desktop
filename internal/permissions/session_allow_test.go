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
	if DefaultSessionKey(editAction("/ws/a.go")) == DefaultSessionKey(editAction("/ws/b.go")) {
		t.Fatal("different mutation targets must produce different keys")
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
	if key := DefaultSessionKey(bashAction(CommandCategoryUnknown)); key != "" {
		t.Fatalf("unknown command key = %q, want empty", key)
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
