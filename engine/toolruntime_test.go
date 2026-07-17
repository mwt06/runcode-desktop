package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/tools/bash"
	"github.com/wt68/runcode/engine/turn"
)

// nilRuntimeRoster is the exact tool roster (names, in order) a Build with a
// nil ToolRuntime must expose for a session with no MCP servers and no
// ExtraTools: the built-ins assembled by the local runtime, then the always-on
// infra tools (Skill, Task, Remember). It is hardcoded on purpose — a refactor
// that silently drops or reorders a built-in must fail here, not ship.
var nilRuntimeRoster = []string{
	"Read",
	"Write",
	"Edit",
	"Delete",
	"Glob",
	"Grep",
	"Bash",
	"BashOutput",
	"KillShell",
	"TodoWrite",
	"WebFetch",
	"WebSearch",
	"Analyze",
	"AskUser",
	"Skill",
	"Task",
	"Remember",
}

func toolNames(list []turn.ToolDescriptor) []string {
	names := make([]string, 0, len(list))
	for _, d := range list {
		names = append(names, d.Name)
	}
	return names
}

// stubTool is a minimal tool.Tool a fake Toolset can hand to Build, standing in
// for a sandbox-forwarded tool.
type stubTool struct{ name string }

func (s stubTool) Name() string        { return s.name }
func (s stubTool) Description() string { return s.name + " stub" }
func (s stubTool) InputSchema() tool.Schema {
	return tool.Schema{Type: tool.SchemaTypeObject}
}
func (s stubTool) IsConcurrencySafe() bool { return true }
func (s stubTool) Run(context.Context, json.RawMessage, *tool.Context, chan<- tool.Event) (tool.Result, error) {
	return tool.Result{}, nil
}

// fakeToolset counts Close calls so ownership tests can assert Session.Close
// closes the provisioned toolset exactly once.
type fakeToolset struct {
	mu     sync.Mutex
	tools  []tool.Tool
	closed int
}

func (ts *fakeToolset) Tools() []tool.Tool { return ts.tools }

func (ts *fakeToolset) Close(context.Context) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.closed++
	return nil
}

func (ts *fakeToolset) closeCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.closed
}

// fakeToolRuntime records every interaction the engine has with it. calls is
// the full interaction log — the ownership test asserts it is exactly one
// Provision and nothing else, pinning down that the engine never tries to
// manage (close) a caller-owned runtime.
type fakeToolRuntime struct {
	mu      sync.Mutex
	calls   []string
	specs   []ToolsetSpec
	toolset *fakeToolset
	err     error
}

func (rt *fakeToolRuntime) Provision(_ context.Context, spec ToolsetSpec) (Toolset, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.calls = append(rt.calls, "Provision")
	rt.specs = append(rt.specs, spec)
	if rt.err != nil {
		return nil, rt.err
	}
	return rt.toolset, nil
}

func (rt *fakeToolRuntime) interactions() ([]string, []ToolsetSpec) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return append([]string(nil), rt.calls...), append([]ToolsetSpec(nil), rt.specs...)
}

// A nil ToolRuntime must reproduce the historical assembly exactly: the
// session's tool roster is byte-for-byte the pre-port one.
func TestBuildNilToolRuntimeRosterUnchanged(t *testing.T) {
	useBuildTestProvider()

	session, err := Build(buildTestConfig(t), Options{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer session.Close(context.Background())

	if got := toolNames(session.ToolList()); !reflect.DeepEqual(got, nilRuntimeRoster) {
		t.Fatalf("nil-runtime tool roster = %v, want %v", got, nilRuntimeRoster)
	}
}

// The local runtime's WebProxy/ToolEnv paths (shared proxied web client,
// injected shell env) must keep building successfully with the same roster.
func TestBuildNilToolRuntimeWithProxyAndToolEnv(t *testing.T) {
	useBuildTestProvider()

	for name, mutate := range map[string]func(*Config){
		"web proxy": func(cfg *Config) { cfg.WebProxy = "http://127.0.0.1:38080" },
		"tool env":  func(cfg *Config) { cfg.ToolEnv = map[string]string{"HOME": t.TempDir()} },
	} {
		cfg := buildTestConfig(t)
		mutate(&cfg)
		session, err := Build(cfg, Options{})
		if err != nil {
			t.Fatalf("Build with %s: %v", name, err)
		}
		if got := toolNames(session.ToolList()); !reflect.DeepEqual(got, nilRuntimeRoster) {
			t.Errorf("tool roster with %s = %v, want %v", name, got, nilRuntimeRoster)
		}
		if err := session.Close(context.Background()); err != nil {
			t.Fatalf("Close with %s: %v", name, err)
		}
	}
}

// An injected runtime replaces the built-in assembly: its toolset's tools reach
// the session, and Provision receives a spec mirroring the session's Config
// (and the host's ShellBudget verbatim).
func TestBuildInjectedToolRuntimeProvisionsSessionTools(t *testing.T) {
	useBuildTestProvider()

	budget := bash.NewBudget(4)
	cfg := buildTestConfig(t)
	cfg.WebProxy = "http://proxy.internal:3128"
	cfg.ToolEnv = map[string]string{"HOME": "/sandbox/home", "HTTPS_PROXY": "http://proxy.internal:3128"}

	toolset := &fakeToolset{tools: []tool.Tool{stubTool{name: "SandboxBash"}, stubTool{name: "SandboxRead"}}}
	runtime := &fakeToolRuntime{toolset: toolset}
	session, err := Build(cfg, Options{ToolRuntime: runtime, ShellBudget: budget})
	if err != nil {
		t.Fatalf("Build with injected runtime: %v", err)
	}
	defer session.Close(context.Background())

	names := toolNames(session.ToolList())
	listed := make(map[string]bool, len(names))
	for _, n := range names {
		listed[n] = true
	}
	for _, want := range []string{"SandboxBash", "SandboxRead"} {
		if !listed[want] {
			t.Errorf("session tools %v missing injected runtime's %q", names, want)
		}
	}

	calls, specs := runtime.interactions()
	if len(calls) != 1 || len(specs) != 1 {
		t.Fatalf("runtime interactions = %v (%d specs), want exactly one Provision", calls, len(specs))
	}
	spec := specs[0]
	if spec.CWD != cfg.CWD {
		t.Errorf("spec.CWD = %q, want %q", spec.CWD, cfg.CWD)
	}
	if spec.WebProxy != cfg.WebProxy {
		t.Errorf("spec.WebProxy = %q, want %q", spec.WebProxy, cfg.WebProxy)
	}
	if !reflect.DeepEqual(spec.ToolEnv, cfg.ToolEnv) {
		t.Errorf("spec.ToolEnv = %v, want %v", spec.ToolEnv, cfg.ToolEnv)
	}
	if spec.ShellBudget != budget {
		t.Errorf("spec.ShellBudget = %p, want the injected budget %p", spec.ShellBudget, budget)
	}
}

// Ownership is two-level: the session-owned Toolset is closed exactly once by
// Session.Close, while the caller-owned runtime sees no interaction beyond the
// single Provision — the engine must never manage its lifecycle.
func TestSessionCloseClosesToolsetButNotRuntime(t *testing.T) {
	useBuildTestProvider()

	toolset := &fakeToolset{tools: []tool.Tool{stubTool{name: "SandboxBash"}}}
	runtime := &fakeToolRuntime{toolset: toolset}
	session, err := Build(buildTestConfig(t), Options{ToolRuntime: runtime})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if got := toolset.closeCount(); got != 0 {
		t.Fatalf("toolset closed %d times before Session.Close, want 0", got)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := toolset.closeCount(); got != 1 {
		t.Fatalf("toolset closed %d times after Session.Close, want exactly 1", got)
	}
	if calls, _ := runtime.interactions(); !reflect.DeepEqual(calls, []string{"Provision"}) {
		t.Fatalf("runtime interactions = %v, want [Provision] only (runtime is caller-owned)", calls)
	}
}

// A failing Provision aborts Build with its error and cleans up the resources
// opened before it: the per-session store is closed while the injected backend
// itself (host-owned, like an injected runtime) stays open.
func TestBuildToolRuntimeProvisionFailureCleansUp(t *testing.T) {
	useBuildTestProvider()

	provisionErr := errors.New("tool gateway unavailable")
	runtime := &fakeToolRuntime{err: provisionErr}
	backend := &fakeBackend{}
	session, err := Build(buildTestConfig(t), Options{ToolRuntime: runtime, Backend: backend})
	if !errors.Is(err, provisionErr) {
		t.Fatalf("Build error = %v, want the Provision error %v", err, provisionErr)
	}
	if session != nil {
		t.Fatal("Build returned a session alongside the Provision error")
	}
	if !backend.allStoresClosed() {
		t.Fatal("per-session store left open after Provision failure; Build must clean up prior resources")
	}
	if backend.wasClosed() {
		t.Fatal("failed Build closed the injected backend; the host owns its lifecycle")
	}
}
