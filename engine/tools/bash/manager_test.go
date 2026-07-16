package bash

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestManagerStartReadsOutputAndExits(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	id, err := mgr.Start("echo hello-bg", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Output reads incrementally (each call consumes new output), so accumulate
	// across polls while waiting for the shell to finish.
	var collected strings.Builder
	var lastExit int
	waitFor(t, func() bool {
		s, _ := mgr.Output(id)
		collected.WriteString(s.NewOutput)
		lastExit = s.ExitCode
		return !s.Running
	})
	if lastExit != 0 {
		t.Fatalf("exit code = %d, want 0", lastExit)
	}
	if !strings.Contains(collected.String(), "hello-bg") {
		t.Fatalf("output = %q, want it to contain hello-bg", collected.String())
	}
}

func TestManagerOutputIsIncremental(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	id, err := mgr.Start("echo one", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, func() bool {
		s, _ := mgr.Output(id)
		return strings.Contains(s.NewOutput, "one") || !s.Running
	})
	// A second read returns no already-consumed output.
	second, _ := mgr.Output(id)
	if strings.Contains(second.NewOutput, "one") {
		t.Fatalf("second read returned already-consumed output: %q", second.NewOutput)
	}
}

func TestManagerKill(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	t.Cleanup(func() { _ = mgr.Close(context.Background()) })

	id, err := mgr.Start("sleep 30", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, err := mgr.Kill(id)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if status.Running {
		t.Fatal("shell should be stopped after Kill")
	}
}

func TestManagerUnknownShell(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	if _, err := mgr.Output("bash_999"); err == nil {
		t.Fatal("Output on unknown shell should error")
	}
	if _, err := mgr.Kill("bash_999"); err == nil {
		t.Fatal("Kill on unknown shell should error")
	}
}

func TestManagerCloseStopsShells(t *testing.T) {
	t.Parallel()
	mgr := NewManager()
	if _, err := mgr.Start("sleep 30", t.TempDir(), nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := mgr.Close(context.Background()); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := mgr.Start("echo x", t.TempDir(), nil); err == nil {
		t.Fatal("Start after Close should error")
	}
}
