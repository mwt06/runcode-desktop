package desktop

import (
	"errors"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/skill"
)

// wireError must pass host command errors (*protocol.Error) through untouched,
// map the desktop sentinels to their codes, and wrap everything else as an
// internal error carrying the original message.
func TestWireErrorMapping(t *testing.T) {
	t.Parallel()
	if wireError(nil) != nil {
		t.Fatal("wireError(nil) must be nil")
	}

	// Host errors pass through as the same value (pointer identity keeps
	// errors.Is working against the host sentinels).
	if got := wireError(host.ErrBusy); got != host.ErrBusy { //nolint:errorlint // identity is the assertion
		t.Fatalf("wireError(host.ErrBusy) = %#v, want the sentinel itself", got)
	}

	cases := []struct {
		in   error
		code string
	}{
		{errNoSession, protocol.ErrCodeNoSession},
		{errBusy, protocol.ErrCodeBusy},
		{errors.New("boom"), protocol.ErrCodeInternal},
	}
	for _, tc := range cases {
		var pe *protocol.Error
		got := wireError(tc.in)
		if !errors.As(got, &pe) {
			t.Fatalf("wireError(%v) = %T, want *protocol.Error", tc.in, got)
		}
		if pe.Code != tc.code {
			t.Errorf("wireError(%v) code = %q, want %q", tc.in, pe.Code, tc.code)
		}
		if pe.Message != tc.in.Error() {
			t.Errorf("wireError(%v) message = %q, want original message", tc.in, pe.Message)
		}
	}
}

// configureSession must assemble the desktop's per-session wiring: an
// interactive permission service around the session's approver, the
// open_preview extra tool, and a fresh edit store bound to the session and
// parked as pending (published to the App only once Create succeeds).
func TestConfigureSessionWiresOptions(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()

	sctx := host.SessionContext{
		ID:       "sess_cfg",
		Approver: host.NewAsyncApprover(func(string, any) {}, ws),
		Emit:     func(string, any) {},
	}
	cfg := engine.Config{CWD: ws, PermissionMode: "interactive"}
	var opts engine.Options
	app.configureSession(sctx, &cfg, &opts)

	if opts.Permissions == nil {
		t.Fatal("configureSession did not install a permission service")
	}
	toolNames := make([]string, len(opts.ExtraTools))
	for i, tl := range opts.ExtraTools {
		toolNames[i] = tl.Name()
	}
	if len(toolNames) != 3 || toolNames[0] != "open_preview" || toolNames[1] != "ReadOffice" || toolNames[2] != "plan_write" {
		t.Fatalf("ExtraTools = %v, want [open_preview ReadOffice plan_write]", toolNames)
	}
	// The planning run is parked like the edit store: bound to this session, and
	// published to the App only once Create succeeds.
	app.mu.Lock()
	pendingPlan := app.pendingPlans
	app.mu.Unlock()
	if pendingPlan == nil {
		t.Fatal("configureSession did not park a plan store")
	}
	if got := pendingPlan.Snapshot().State; got != PlanStateIdle {
		t.Fatalf("fresh plan run state = %q, want idle", got)
	}
	// The Skill tool is a replacement, not an extra: it must arrive through the
	// engine's port under the model-facing name the engine requires.
	if opts.SkillTool == nil {
		t.Fatal("configureSession did not install the desktop's Skill tool")
	}
	if got := opts.SkillTool.Name(); got != skill.ToolName {
		t.Fatalf("SkillTool name = %q, want %q", got, skill.ToolName)
	}
	es, ok := opts.EditRecorder.(*editStore)
	if !ok || es == nil {
		t.Fatalf("EditRecorder type = %T, want *editStore", opts.EditRecorder)
	}
	app.mu.Lock()
	pending := app.pendingEdits
	active := app.edits
	app.mu.Unlock()
	if pending != es {
		t.Fatal("the new edit store was not parked as pendingEdits")
	}
	if active == es {
		t.Fatal("the pending edit store leaked into App.edits before Create succeeded")
	}
	// The store is bound to the session's edit directory (BeginSession ran).
	es.mu.Lock()
	boundWS, dir := es.ws, es.dir
	es.mu.Unlock()
	if boundWS != ws || dir == "" {
		t.Fatalf("edit store bound to (%q, %q), want workspace %q with a session dir", boundWS, dir, ws)
	}
}
