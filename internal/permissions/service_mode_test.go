package permissions

import "testing"

func TestServiceSetModeSwitches(t *testing.T) {
	t.Parallel()
	// An interactive authorizer makes the service mode-aware and enables
	// runtime switching.
	svc := NewService(Options{
		InteractiveAuthorizer: NonInteractiveAuthorizer{},
		Mode:                  "safe",
	})

	if svc.Mode() != "safe" {
		t.Fatalf("initial mode = %q, want safe", svc.Mode())
	}
	if svc.ApprovalAvailable() {
		t.Fatal("safe mode should not report approval available")
	}

	if err := svc.SetMode("interactive"); err != nil {
		t.Fatalf("SetMode interactive: %v", err)
	}
	if svc.Mode() != "interactive" {
		t.Fatalf("mode = %q, want interactive", svc.Mode())
	}
	if !svc.ApprovalAvailable() {
		t.Fatal("interactive mode should report approval available")
	}

	if err := svc.SetMode("bogus"); err == nil {
		t.Fatal("unknown mode should error")
	}
}

func TestServiceSetModeInteractiveRequiresApprover(t *testing.T) {
	t.Parallel()
	// No interactive authorizer: switching to interactive is unavailable.
	svc := NewService(Options{Mode: "safe"})
	if err := svc.SetMode("interactive"); err == nil {
		t.Fatal("interactive without an approver should error")
	}
	// Switching to safe is always fine.
	if err := svc.SetMode("safe"); err != nil {
		t.Fatalf("SetMode safe: %v", err)
	}
}
