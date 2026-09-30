package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/browsertool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

func TestBrowserSessionPermissionsAndRequest(t *testing.T) {
	for _, mode := range []string{"interactive", "judge", "safe", "plan", "denied"} {
		t.Run(mode, func(t *testing.T) { runBrowserSession(t, mode, false) })
	}
}

// Opt-in only: this test actually opens a harmless local page in the OS browser.
func TestBrowserNativeSmoke(t *testing.T) {
	if os.Getenv("RUNCODE_BROWSER_SMOKE") != "1" {
		t.Skip("set RUNCODE_BROWSER_SMOKE=1 to open a local test page")
	}
	runBrowserSession(t, "interactive", true)
}

func runBrowserSession(t *testing.T, mode string, native bool) {
	t.Helper()
	isolateConfigDir(t)
	var opened, approvals, requests atomic.Int32
	pageRequested := make(chan struct{}, 1)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/confirm" && r.URL.Query().Get("stage") == "2" {
			select {
			case pageRequested <- struct{}{}:
			default:
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<title>Local browser tool check</title><h1>Browser opening verified</h1><p>This isolated test page has no account or project data. You can close this tab.</p>`)
	}))
	defer page.Close()
	target := page.URL + "/confirm?stage=2&note=test%20page#review"
	input, _ := json.Marshal(map[string]string{"url": target})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		calls := requests.Add(1)
		delta := map[string]any{"content": "The open request was handled; this is not user confirmation."}
		finish := "stop"
		if calls == 1 {
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "local-browser", "type": "function", "function": map[string]any{"name": browsertool.Name, "arguments": string(input)}}}}
			finish = "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}},
			map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
		} {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer model.Close()
	app := New(&recordingSink{})
	app.workspace = "different-focused-workspace"
	cfg := engine.Config{CWD: t.TempDir(), SessionID: "browser-fixture", Provider: "openai", Model: "browser-fixture", BaseURL: model.URL + "/v1", APIKey: "fixture", PermissionMode: mode, MaxIterations: 3, SystemPromptAppend: "host-append-sentinel"}
	if mode == "plan" || mode == "denied" {
		cfg.PermissionMode = "interactive"
	}

	var approver *host.AsyncApprover
	approver = host.NewAsyncApprover(func(event string, payload any) {
		if event != EventPermissionRequest {
			return
		}
		req := payload.(PermissionRequest)
		if opened.Load() != 0 {
			t.Error("browser opened before approval")
		}
		if req.Summary.ToolName != browsertool.Name {
			t.Errorf("unexpected approval: %+v", req)
		}
		if req.Summary.NetworkHost != strings.TrimPrefix(page.URL, "http://") {
			t.Errorf("approval hides target host/port: %+v", req.Summary)
		}
		if strings.Contains(req.Summary.CommandSummary, "stage=") {
			t.Error("raw URL leaked into telemetry")
		}
		approvals.Add(1)
		decision := "allow-once"
		if mode == "denied" {
			decision = "deny"
		}
		if err := approver.Resolve(req.ID, decision); err != nil {
			t.Error(err)
		}
	}, cfg.CWD)
	defer approver.DenyAll()

	opts := engine.Options{}
	app.configureSession(host.SessionContext{ID: cfg.SessionID, Approver: approver, Emit: func(string, any) {}}, &cfg, &opts)
	if hostToolClasses[browsertool.Name] != permissions.ClassMutating {
		t.Fatal("missing mutating classification")
	}
	found := false
	for i, extra := range opts.ExtraTools {
		if extra.Name() != browsertool.Name {
			continue
		}
		found = true
		// Replace only the OS side effect; the configured permission service and
		// actual engine dispatch remain in the loop. Native smoke uses the real OS.
		opts.ExtraTools[i] = browsertool.New(func(ctx context.Context, url string) error {
			if approvals.Load() != 1 || url != target {
				t.Errorf("approval=%d url=%q", approvals.Load(), url)
			}
			opened.Add(1)
			if native {
				return openBrowserContext(ctx, url)
			}
			return nil
		})
	}
	if !found {
		t.Fatal("tool not registered")
	}
	observed := 0
	opts.LLMRequestObserver = func(_, _ string, req llm.Request) {
		observed++
		var system strings.Builder
		hasTool := false
		for _, b := range req.System {
			system.WriteString(b.Text)
			system.WriteByte('\n')
		}
		for _, tool := range req.Tools {
			if tool.Name == browsertool.Name {
				hasTool = true
			}
		}
		text := system.String()
		for _, want := range []string{cfg.CWD, "host-append-sentinel", "directory returned by Skill", "Operating system: " + runtime.GOOS, "Process architecture: " + runtime.GOARCH, "Desktop interaction:", "run_in_background"} {
			if !strings.Contains(text, want) {
				t.Errorf("request missing %q", want)
			}
		}
		if !hasTool || strings.Contains(text, app.workspace) {
			t.Error("tool missing or workspace taken from focus")
		}
	}
	session, err := engine.Build(cfg, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close(context.Background())
	if mode == "plan" {
		session.SetPlanMode(true)
	}
	if _, err := session.RunTurn(context.Background(), "Open the local confirmation page"); err != nil {
		t.Fatal(err)
	}
	wantOpen := int32(0)
	wantApproval := int32(0)
	if mode == "interactive" || mode == "judge" {
		wantOpen = 1
		wantApproval = 1
	}
	if mode == "denied" {
		wantApproval = 1
	}
	wantObserved := 2
	if mode == "denied" {
		wantObserved = 1
	}
	if opened.Load() != wantOpen || approvals.Load() != wantApproval || observed != wantObserved {
		t.Fatalf("opened=%d approvals=%d observed=%d", opened.Load(), approvals.Load(), observed)
	}
	if native {
		select {
		case <-pageRequested:
			t.Log("real system browser requested the local confirmation URL")
		case <-time.After(20 * time.Second):
			t.Fatal("OS accepted opening, but no page request arrived")
		}
	}
}
