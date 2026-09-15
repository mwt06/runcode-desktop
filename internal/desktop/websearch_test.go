package desktop

import (
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/websearch"
)

// The search endpoint is derived from the session's Bridge base URL, so the
// selected tenant prefix comes along for free — search is billed to the same
// tenant as the conversation.
func TestWebSearchEndpointKeepsTenantPrefix(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://bridge.example/v1", "https://bridge.example/v1/websearch"},
		{"https://bridge.example/t/t-42/v1", "https://bridge.example/t/t-42/v1/websearch"},
		{"https://bridge.example/v1/", "https://bridge.example/v1/websearch"},
		{"   ", ""},
	} {
		if got := webSearchEndpoint(tc.base); got != tc.want {
			t.Errorf("webSearchEndpoint(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// A passport session swaps in the platform's search; the replacement must answer
// to the built-in's name or the engine refuses the whole session.
func TestPassportWebSearchReplacesBuiltin(t *testing.T) {
	cfg := &engine.Config{
		BaseURL:     "https://bridge.example/t/t-1/v1",
		TokenSource: func() (string, error) { return "AT", nil },
	}
	tl := passportWebSearch(cfg)
	if tl == nil {
		t.Fatal("a passport session got no platform search tool")
	}
	if got := tl.Name(); got != websearch.ToolName {
		t.Fatalf("tool name = %q, want %q", got, websearch.ToolName)
	}
}

// Direct connections (a self-entered endpoint, a custom model) keep the engine's
// built-in search: they have neither a Bridge nor a user token, and their config
// deliberately carries no TokenSource so no login credential reaches a
// third-party endpoint.
func TestDirectConnectionKeepsBuiltinWebSearch(t *testing.T) {
	if tl := passportWebSearch(&engine.Config{BaseURL: "https://api.example/v1", APIKey: "sk-x"}); tl != nil {
		t.Fatal("a direct connection was given the platform search tool")
	}
	if tl := passportWebSearch(nil); tl != nil {
		t.Fatal("a nil config produced a search tool")
	}
}

// configureSession installs the replacement through the engine's port — never as
// an extra tool, which would both fail assembly on the duplicate name and leave
// sub-agents on a different search backend.
func TestConfigureSessionInstallsWebSearchOnlyForPassport(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	sctx := host.SessionContext{
		ID:       "sess_ws",
		Approver: host.NewAsyncApprover(func(string, any) {}, ws),
		Emit:     func(string, any) {},
	}

	direct := engine.Config{CWD: ws, PermissionMode: "interactive", BaseURL: "https://api.example/v1"}
	var opts engine.Options
	app.configureSession(sctx, &direct, &opts)
	if opts.WebSearchTool != nil {
		t.Fatal("a direct connection replaced the built-in WebSearch")
	}

	passport := engine.Config{
		CWD:            ws,
		PermissionMode: "interactive",
		BaseURL:        "https://bridge.example/v1",
		TokenSource:    func() (string, error) { return "AT", nil },
	}
	opts = engine.Options{}
	app.configureSession(sctx, &passport, &opts)
	if opts.WebSearchTool == nil {
		t.Fatal("a passport session did not get the platform search tool")
	}
	if got := opts.WebSearchTool.Name(); got != websearch.ToolName {
		t.Fatalf("WebSearchTool name = %q, want %q", got, websearch.ToolName)
	}
	for _, tl := range opts.ExtraTools {
		if tl.Name() == websearch.ToolName {
			t.Fatal("WebSearch arrived through ExtraTools; it must use the replacement port")
		}
	}
}
