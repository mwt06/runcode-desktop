package bash

import (
	"context"
	"strings"
	"testing"
)

func TestBudgetTryAcquireRelease(t *testing.T) {
	t.Parallel()

	b := NewBudget(1)
	if !b.TryAcquire() {
		t.Fatal("first acquire on budget(1) should succeed")
	}
	if b.TryAcquire() {
		t.Fatal("second acquire on budget(1) should fail fast")
	}
	b.Release()
	if !b.TryAcquire() {
		t.Fatal("acquire after release should succeed")
	}

	// nil budget = no global cap: everything is admitted, releases are no-ops.
	var none *Budget
	if !none.TryAcquire() {
		t.Fatal("nil budget must admit")
	}
	none.Release()
}

// One Budget shared by two managers caps background shells across both — the
// per-session (per-manager) limit alone would admit far more.
func TestBudgetCapsShellsAcrossManagers(t *testing.T) {
	t.Parallel()

	budget := NewBudget(2)
	m1 := NewManagerWithBudget(budget)
	m2 := NewManagerWithBudget(budget)
	// defer (not t.Cleanup): the shells' workspaces are t.TempDir()s whose
	// removal cleanups run before an outer Cleanup would close the managers, and
	// Windows cannot delete a directory a live shell holds as its cwd.
	defer func() {
		_ = m1.Close(context.Background())
		_ = m2.Close(context.Background())
	}()

	if _, err := m1.Start("sleep 30", t.TempDir(), nil); err != nil {
		t.Fatalf("first start: %v", err)
	}
	id2, err := m2.Start("sleep 30", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}

	_, err = m1.Start("sleep 30", t.TempDir(), nil)
	if err == nil {
		t.Fatal("third shell should exceed the global budget of 2")
	}
	if !strings.Contains(err.Error(), "global max 2") || !strings.Contains(err.Error(), "kill one") {
		t.Fatalf("budget error %q should name the global cap and suggest killing a shell", err)
	}

	// Killing a shell (in the other session) frees its slot; a retry succeeds.
	if _, err := m2.Kill(id2); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, err := m1.Start("sleep 30", t.TempDir(), nil); err != nil {
		t.Fatalf("start after kill should reuse the freed slot: %v", err)
	}
}

// Closing a manager releases every slot its shells held, so surviving sessions
// get the capacity back.
func TestManagerCloseReleasesBudget(t *testing.T) {
	t.Parallel()

	budget := NewBudget(2)
	m1 := NewManagerWithBudget(budget)
	if _, err := m1.Start("sleep 30", t.TempDir(), nil); err != nil {
		t.Fatalf("start 1: %v", err)
	}
	if _, err := m1.Start("sleep 30", t.TempDir(), nil); err != nil {
		t.Fatalf("start 2: %v", err)
	}
	if err := m1.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}

	m2 := NewManagerWithBudget(budget)
	defer func() { _ = m2.Close(context.Background()) }()
	if _, err := m2.Start("echo ok", t.TempDir(), nil); err != nil {
		t.Fatalf("start after Close should find the budget fully released: %v", err)
	}
	if _, err := m2.Start("echo ok2", t.TempDir(), nil); err != nil {
		t.Fatalf("second start after Close: %v", err)
	}
}

// A shell that exits on its own returns its slot without any Kill/Close.
func TestBudgetReleasedOnNaturalExit(t *testing.T) {
	t.Parallel()

	budget := NewBudget(1)
	mgr := NewManagerWithBudget(budget)
	defer func() { _ = mgr.Close(context.Background()) }()

	id, err := mgr.Start("echo done", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, func() bool {
		s, _ := mgr.Output(id)
		return !s.Running
	})
	// The slot is released just before the shell's done channel closes; poll for
	// the successor start rather than racing that hand-off.
	waitFor(t, func() bool { return budget.TryAcquire() })
	budget.Release()
}
