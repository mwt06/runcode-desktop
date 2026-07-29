package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// TestPassportInjectionEndToEndFromConfig walks the exact chain that decides
// whether a platform MCP server authenticates: the passport flag in the user's
// config.toml → loadDesktopMCP → attachMCPPassport → the headers the transport
// puts on every request. A regression here is what makes the OA server answer
// 401, and it is invisible in the per-function tests above.
func TestPassportInjectionEndToEndFromConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APPDATA", home) // os.UserConfigDir() on Windows
	t.Setenv("XDG_CONFIG_HOME", home)
	cfgDir := filepath.Join(home, "runcode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const cfg = `
[mcp.servers.oa]
transport = 'http'
url = 'http://oa.example/mcp'
passport = true

[mcp.servers.third]
transport = 'http'
url = 'http://third.example/mcp'
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, _ := loadDesktopMCP(t.TempDir())
	if len(servers) != 2 {
		t.Fatalf("loadDesktopMCP returned %d servers, want 2", len(servers))
	}

	app := &App{tokens: newTokenManager("", "", nil, nil), passportTenant: "t-9"}
	app.tokens.ts = tokenSet{AccessToken: "LIVE-TOKEN", Expiry: time.Now().Add(time.Hour)}
	app.attachMCPPassport(servers)

	byName := map[string]mcp.ServerConfig{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	oa, third := byName["oa"], byName["third"]
	if oa.HeaderSource == nil {
		t.Fatal("oa has no HeaderSource: passport=true in config.toml never reached the transport (this is the 401)")
	}
	h, err := oa.HeaderSource()
	if err != nil {
		t.Fatalf("oa HeaderSource: %v", err)
	}
	if h["Authorization"] != "Bearer LIVE-TOKEN" {
		t.Fatalf("Authorization = %q, want Bearer LIVE-TOKEN", h["Authorization"])
	}
	if h["X-Tenant-Id"] != "t-9" {
		t.Fatalf("X-Tenant-Id = %q, want t-9", h["X-Tenant-Id"])
	}
	if third.HeaderSource != nil {
		t.Fatal("a server without passport=true must never receive the user's token")
	}
}

func TestApplyMCPPassportOnlyTouchesNamedServers(t *testing.T) {
	servers := []mcp.ServerConfig{{Name: "oa"}, {Name: "third-party"}}
	sentinel := func() (map[string]string, error) { return map[string]string{"X": "1"}, nil }
	applyMCPPassport(servers, map[string]bool{"oa": true}, sentinel)

	if servers[0].HeaderSource == nil {
		t.Fatal("oa should have a HeaderSource attached")
	}
	if servers[1].HeaderSource != nil {
		t.Fatal("third-party must not get a HeaderSource (the token must not leak to it)")
	}
	// The same fact (platform-built) also waives the per-call approval prompt.
	if !servers[0].Trusted {
		t.Fatal("oa should be Trusted so its tool calls do not prompt for approval")
	}
	if servers[1].Trusted {
		t.Fatal("a third-party server must never be trusted (its calls must still prompt)")
	}
	got, err := servers[0].HeaderSource()
	if err != nil || got["X"] != "1" {
		t.Fatalf("HeaderSource() = %v, %v; want the sentinel headers", got, err)
	}
}

func TestPassportHeadersInjectsTokenAndTenant(t *testing.T) {
	h, err := passportHeaders(func() (string, error) { return "abc", nil }, "t-1")
	if err != nil {
		t.Fatalf("passportHeaders: %v", err)
	}
	if h["Authorization"] != "Bearer abc" {
		t.Fatalf("Authorization = %q, want Bearer abc", h["Authorization"])
	}
	if h["X-Tenant-Id"] != "t-1" {
		t.Fatalf("X-Tenant-Id = %q, want t-1", h["X-Tenant-Id"])
	}
}

func TestPassportHeadersOmitsTenantWhenEmpty(t *testing.T) {
	h, err := passportHeaders(func() (string, error) { return "abc", nil }, "   ")
	if err != nil {
		t.Fatalf("passportHeaders: %v", err)
	}
	if _, ok := h["X-Tenant-Id"]; ok {
		t.Fatal("X-Tenant-Id must be omitted for an empty tenant")
	}
}

func TestPassportHeadersPropagatesTokenError(t *testing.T) {
	_, err := passportHeaders(func() (string, error) { return "", errors.New("not logged in") }, "t-1")
	if err == nil || err.Error() != "not logged in" {
		t.Fatalf("err = %v, want the token error to propagate (no anonymous request)", err)
	}
}

// TestMarketInstallPersistsPassportFlag proves the install path a market click
// takes (SaveMCPServer with the entry's passport flag) actually lands in
// config.toml — so a platform-built server authenticates without anyone editing
// anything by hand.
func TestMarketInstallPersistsPassportFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("APPDATA", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	app := &App{}
	// Exactly what the market's 安装 button sends for the OA entry.
	if err := app.SaveMCPServer(MCPServerInput{
		Name: "oa", Transport: "http", URL: "http://123.249.111.75:8101/mcp",
		Passport: true, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMCPServer: %v", err)
	}

	servers, err := app.loadMCPServers()
	if err != nil {
		t.Fatalf("loadMCPServers: %v", err)
	}
	oa, ok := servers["oa"]
	if !ok {
		t.Fatal("oa was not written to config.toml")
	}
	if oa.Passport == nil || !*oa.Passport {
		t.Fatal("install did not persist passport=true — the server would stay anonymous (401)")
	}

	// And a third-party install must stay anonymous.
	if err := app.SaveMCPServer(MCPServerInput{
		Name: "third", Transport: "http", URL: "http://third.example/mcp", Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMCPServer(third): %v", err)
	}
	servers, _ = app.loadMCPServers()
	if p := servers["third"].Passport; p != nil && *p {
		t.Fatal("a non-platform server must not be marked for token injection")
	}
}

// TestSyncMarketOnceSkipsWhenLoggedOutOrAlreadySynced pins the once-per-run
// contract: the market decides which servers carry the user's identity, but it
// changes on the platform's schedule, not per session — so opening a session
// must never pay for a fetch. A logged-out app fetches nothing at all.
func TestSyncMarketOnceSkipsWhenLoggedOutOrAlreadySynced(t *testing.T) {
	// Logged out (no token): must not attempt a fetch, and must not mark synced —
	// a later login has to be able to retry.
	app := &App{tokens: newTokenManager("", "", nil, nil)}
	app.syncMarketOnce()
	if app.marketSynced {
		t.Fatal("a logged-out sync must stay unmarked so a later login retries")
	}

	// Already synced this run: returns without touching the network even when a
	// token is present (a fetch here would panic on the nil HTTP client).
	app.tokens.ts = tokenSet{AccessToken: "LIVE", Expiry: time.Now().Add(time.Hour)}
	app.marketSynced = true
	app.syncMarketOnce()
}

// TestConfigureSessionTrustsPlatformMCPServers pins the wiring that actually
// decides whether the OA server prompts. The desktop installs its own
// permissions.Service (to add the harm judge), which bypasses the engine's
// default policy wiring — so the trusted-server set must be declared here as
// well. Miss it and every platform MCP call prompts for approval even though the
// server is marked Trusted, which is invisible in the per-layer tests.
func TestConfigureSessionTrustsPlatformMCPServers(t *testing.T) {
	app := &App{}
	cfg := engine.Config{
		PermissionMode: "safe", // the strictest mode: nothing interactive can rescue a wrong policy
		MCPServers: []mcp.ServerConfig{
			{Name: "oa", Transport: mcp.TransportHTTP, URL: "http://oa/mcp", Trusted: true},
			{Name: "third", Transport: mcp.TransportHTTP, URL: "http://third/mcp"},
		},
	}
	opts := engine.Options{}
	app.configureSession(host.SessionContext{ID: "s1", Emit: func(string, any) {}}, &cfg, &opts)
	if opts.Permissions == nil {
		t.Fatal("configureSession installed no permission service")
	}

	call := func(tool string) permissions.Decision {
		_, d := opts.Permissions.AuthorizeTool(context.Background(), permissions.ResolveRequest{
			ToolName: tool,
			Input:    json.RawMessage(`{}`),
		})
		return d
	}
	if got := call("mcp__oa__my_todo"); got.FinalEffect != permissions.EffectAllow {
		t.Fatalf("platform MCP call effect = %v (reason %v), want allow without approval",
			got.FinalEffect, got.Reason)
	}
	// A third-party server must still be gated — in safe mode that means denied,
	// never silently allowed.
	if got := call("mcp__third__run"); got.FinalEffect == permissions.EffectAllow {
		t.Fatal("a third-party MCP call must not be auto-allowed")
	}
}

// TestReloadMCPServersGuards covers the two cases where applying MCP changes to a
// live session must not rebuild it: no session at all (nothing to do — the next
// session reads the new config), and a turn in flight (rebuilding would drop the
// in-flight tool calls).
func TestReloadMCPServersGuards(t *testing.T) {
	idle := &App{}
	reloaded, err := idle.ReloadMCPServers()
	if err != nil || reloaded {
		t.Fatalf("no session: got (%v, %v), want (false, nil)", reloaded, err)
	}

	busy := &App{currentID: "s1", turnActive: true}
	busy.config.Model = "m"
	reloaded, err = busy.ReloadMCPServers()
	if reloaded || err == nil {
		t.Fatalf("busy session: got (%v, %v), want (false, error explaining the wait)", reloaded, err)
	}
}
