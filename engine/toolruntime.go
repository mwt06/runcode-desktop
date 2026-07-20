package engine

import (
	"context"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/bash"
	"gitlab.ouc-online.com.cn/aibase/agentloop/webclient"
)

// ToolsetSpec carries the per-session parameters a ToolRuntime needs to
// assemble the built-in toolset. Fields mirror the session's Config (and, for
// ShellBudget, Options), so a runtime sees exactly what Build would have used
// to assemble the tools itself.
type ToolsetSpec struct {
	CWD      string
	ToolEnv  map[string]string
	WebProxy string
	// ShellBudget is the host's cross-session background-shell budget
	// (Options.ShellBudget); nil means the per-session limit only.
	ShellBudget *bash.Budget
}

// Toolset is one session's assembled built-in tools plus their session-owned
// resources (e.g. the background-shell manager). Session.Close closes it —
// a Toolset is session-scoped regardless of who provisioned it.
type Toolset interface {
	Tools() []tool.Tool
	Close(ctx context.Context) error
}

// ToolRuntime assembles built-in toolsets. nil = the in-process local runtime
// (historical behavior). A server host may inject a gateway runtime whose
// toolsets mix local tools with sandbox-forwarded ones — tool.Tool is the
// execution-location-independent contract: the permission gate, harm judge,
// read-set and event stream all sit in the executor around Run and do not
// change with where Run executes (the MCP manager is the existing precedent).
// An injected runtime is caller-owned: the engine never closes it — one
// gateway client may serve many sessions, each with its own Toolset.
type ToolRuntime interface {
	Provision(ctx context.Context, spec ToolsetSpec) (Toolset, error)
}

// webProxyClientTimeout and webProxyClientMaxRedirects parameterize the shared
// HTTP client built when Config.WebProxy is set. They mirror the web tools' own
// defaults: both use 5 redirects; the timeout is webfetch's 30s, the larger of
// the two (websearch's is 20s), so the shared client never cuts a tool off
// earlier than its historical limit.
const (
	webProxyClientTimeout      = 30 * time.Second
	webProxyClientMaxRedirects = 5
)

// localRuntime is the in-process ToolRuntime Build falls back to when
// Options.ToolRuntime is nil. It reproduces the historical assembly exactly:
// a per-session background-shell manager plus the built-in tools wired to it.
type localRuntime struct{}

// Provision assembles the local built-in toolset for one session.
func (localRuntime) Provision(_ context.Context, spec ToolsetSpec) (Toolset, error) {
	// Background shells count against the host's cross-session budget when one is
	// injected (nil = per-session limit only, the historical behavior).
	shells := bash.NewManagerWithBudget(spec.ShellBudget)
	toolCfg := tools.Config{ShellEnv: spec.ToolEnv}
	if strings.TrimSpace(spec.WebProxy) != "" {
		// One proxied client is shared by WebFetch and WebSearch. Their own
		// defaults differ only in timeout (30s vs 20s); the shared client uses the
		// larger so neither tool gets stricter than its historical bound. With no
		// WebProxy the client stays nil and each tool keeps its exact default —
		// including the proxy env fallback.
		toolCfg.WebClient = webclient.NewWithProxy(webProxyClientTimeout, webProxyClientMaxRedirects, spec.WebProxy)
	}
	return &localToolset{shells: shells, tools: tools.BuiltinsWithConfig(shells, toolCfg)}, nil
}

// localToolset is one session's locally assembled built-in tools together with
// the background-shell manager they share; Close terminates that manager (and
// with it any still-running background shells).
type localToolset struct {
	shells *bash.Manager
	tools  []tool.Tool
}

func (ts *localToolset) Tools() []tool.Tool { return ts.tools }

func (ts *localToolset) Close(ctx context.Context) error { return ts.shells.Close(ctx) }
