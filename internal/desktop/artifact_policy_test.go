package desktop

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/wt68/runcode/internal/browsertool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/edit"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/read"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/write"
)

func artifactAction(op permissions.Operation, path string, readState string) permissions.Action {
	return permissions.Action{
		ToolName:  map[permissions.Operation]string{permissions.OperationWrite: "Write", permissions.OperationEdit: "Edit"}[op],
		Operation: op,
		Risk:      permissions.RiskHigh,
		Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeWorkspace, Path: path}},
		Metadata:  map[string]any{permissions.MetadataReadState: readState},
	}
}

func assertArtifactWriteNoApproval(t *testing.T, path string) {
	t.Helper()
	p := newArtifactPolicy(permissions.DefaultPolicy{})
	d := p.Decide(context.Background(), artifactAction(permissions.OperationWrite, "/ws/"+path, permissions.ReadStateNotRequired))
	if d.FinalEffect != permissions.EffectAllow || d.Reason != reasonArtifactWrite {
		t.Fatalf("write %q => %+v, want artifact allow", path, d)
	}
}

func TestArtifactSVGNoApproval(t *testing.T)  { assertArtifactWriteNoApproval(t, "poster.svg") }
func TestArtifactJPGNoApproval(t *testing.T)  { assertArtifactWriteNoApproval(t, "photo.jpg") }
func TestArtifactHTMLNoApproval(t *testing.T) { assertArtifactWriteNoApproval(t, "site.html") }
func TestArtifactUnknownExtensionNoApproval(t *testing.T) {
	assertArtifactWriteNoApproval(t, "generated.custom")
}

func TestArtifactEditExistingNoApproval(t *testing.T) {
	p := newArtifactPolicy(permissions.DefaultPolicy{})
	d := p.Decide(context.Background(), artifactAction(permissions.OperationEdit, "/ws/report.unknown", permissions.ReadStateFresh))
	if d.FinalEffect != permissions.EffectAllow || d.Reason != reasonArtifactWrite {
		t.Fatalf("edit => %+v, want artifact allow", d)
	}
}

func TestArtifactPolicyPreservesMutationSafetyBoundaries(t *testing.T) {
	p := newArtifactPolicy(permissions.DefaultPolicy{})
	cases := []struct {
		name   string
		action permissions.Action
		want   permissions.Effect
	}{
		{"outside", permissions.Action{Operation: permissions.OperationWrite, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeOutside, Path: `D:\tmp\poster.png`}}, Metadata: map[string]any{permissions.MetadataReadState: permissions.ReadStateNotRequired}}, permissions.EffectAsk},
		{"git", artifactAction(permissions.OperationWrite, "/ws/.git/config", permissions.ReadStateNotRequired), permissions.EffectAsk},
		{"git directory target", artifactAction(permissions.OperationWrite, "/ws/.git", permissions.ReadStateNotRequired), permissions.EffectAsk},
		{"env", artifactAction(permissions.OperationWrite, "/ws/.env.local", permissions.ReadStateNotRequired), permissions.EffectAsk},
		{"shell rc", artifactAction(permissions.OperationEdit, "/ws/.bashrc", permissions.ReadStateFresh), permissions.EffectAsk},
		{"delete", permissions.Action{Operation: permissions.OperationDelete, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeWorkspace, Path: "/ws/poster.png"}}}, permissions.EffectAsk},
		{"bash", permissions.Action{Operation: permissions.OperationExecute, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceCommand, Scope: permissions.ResourceScopeWorkspace, Path: "echo x > poster.png"}}}, permissions.EffectAsk},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := p.Decide(context.Background(), tc.action).FinalEffect; got != tc.want {
				t.Fatalf("effect=%s, want %s", got, tc.want)
			}
		})
	}
}

func TestArtifactOutsideWorkspaceStillBlocked(t *testing.T) {
	action := permissions.Action{Operation: permissions.OperationWrite, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeOutside, Path: `D:\tmp\poster.png`}}, Metadata: map[string]any{permissions.MetadataReadState: permissions.ReadStateNotRequired}}
	if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action).FinalEffect; got != permissions.EffectAsk {
		t.Fatalf("effect=%s, want ask", got)
	}
}

func TestArtifactExecutionSurfaceStillBlocked(t *testing.T) {
	for _, path := range []string{"/ws/.git/config", "/ws/.env.local", "/ws/.bashrc"} {
		if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), artifactAction(permissions.OperationWrite, path, permissions.ReadStateNotRequired)).FinalEffect; got != permissions.EffectAsk {
			t.Fatalf("%s effect=%s, want ask", path, got)
		}
	}
}

func TestArtifactExecutionSurfaceSymlinkStillBlocked(t *testing.T) {
	ws := t.TempDir()
	if err := os.Mkdir(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(ws, "generated")
	if err := os.Symlink(filepath.Join(ws, ".git"), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	action := artifactAction(permissions.OperationEdit, filepath.Join(alias, "config"), permissions.ReadStateFresh)
	if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action).FinalEffect; got != permissions.EffectAsk {
		t.Fatalf("symlinked execution surface effect=%s, want ask", got)
	}
}

func TestArtifactExecutionSurfaceDanglingSymlinkStillBlocked(t *testing.T) {
	ws := t.TempDir()
	alias := filepath.Join(ws, "generated")
	if err := os.Symlink(filepath.Join(ws, ".env.generated"), alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	action := artifactAction(permissions.OperationWrite, alias, permissions.ReadStateNotRequired)
	if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action).FinalEffect; got != permissions.EffectAsk {
		t.Fatalf("dangling symlink execution surface effect=%s, want ask", got)
	}
}

func TestArtifactPolicyAllowsRoutineSymlinkOutput(t *testing.T) {
	ws := t.TempDir()
	realDir := filepath.Join(ws, "assets-real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(ws, "assets")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	action := artifactAction(permissions.OperationWrite, filepath.Join(alias, "poster.png"), permissions.ReadStateNotRequired)
	if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action).FinalEffect; got != permissions.EffectAllow {
		t.Fatalf("routine symlink output effect=%s, want allow", got)
	}
}

func TestArtifactBashRedirectStillNeedsOriginalPolicy(t *testing.T) {
	action := permissions.Action{Operation: permissions.OperationExecute, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceCommand, Scope: permissions.ResourceScopeWorkspace, Path: "echo x > poster.png"}}}
	if got := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action).FinalEffect; got != permissions.EffectAsk {
		t.Fatalf("effect=%s, want ask", got)
	}
}

func TestArtifactPolicyPreservesReadBeforeWriteDenials(t *testing.T) {
	p := newArtifactPolicy(permissions.DefaultPolicy{})
	for _, state := range []string{permissions.ReadStateMissing, permissions.ReadStatePartial, permissions.ReadStateStale} {
		d := p.Decide(context.Background(), artifactAction(permissions.OperationWrite, "/ws/existing.svg", state))
		if d.FinalEffect != permissions.EffectDeny {
			t.Fatalf("state %q => %+v, want deny", state, d)
		}
	}
}

func TestArtifactWindowsStreamMutationKeepsOriginalPolicy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("NTFS stream aliases are Windows paths")
	}
	for _, name := range []string{".env", ".npmrc", ".gitconfig", ".ENV", "poster.png"} {
		for _, operation := range []string{"create", "overwrite", "edit", "unread overwrite"} {
			t.Run(name+"/"+operation, func(t *testing.T) {
				ws := t.TempDir()
				base := filepath.Join(ws, name)
				path := base + "::$DATA"
				if name == ".ENV" {
					path = base + "::$data"
				}
				original := "original content"
				if operation != "create" {
					if err := os.WriteFile(base, []byte(original), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				tctx := &tool.Context{WorkingDirectory: ws}
				if operation == "overwrite" || operation == "edit" {
					input, _ := json.Marshal(map[string]string{"path": path})
					if _, err := (read.Tool{}).Run(context.Background(), input, tctx, nil); err != nil {
						t.Fatal(err)
					}
				}
				var mutation tool.Tool = write.Tool{}
				input, _ := json.Marshal(map[string]string{"path": path, "content": "changed content"})
				if operation == "edit" {
					mutation = edit.Tool{}
					input, _ = json.Marshal(map[string]string{"path": path, "old_string": original, "new_string": "changed content"})
				}
				action, err := (permissions.DefaultResolver{}).Resolve(context.Background(), permissions.ResolveRequest{ToolName: mutation.Name(), Input: input, Context: tctx})
				if err != nil {
					t.Fatal(err)
				}
				before := (permissions.DefaultPolicy{}).Decide(context.Background(), action)
				after := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action)
				// Execute only a silent approval, exactly the decision that bypassed
				// the protected base file's interactive permission boundary.
				if after.FinalEffect == permissions.EffectAllow {
					if _, err := mutation.Run(context.Background(), input, tctx, nil); err != nil {
						t.Fatal(err)
					}
				}
				if after.FinalEffect != before.FinalEffect {
					t.Errorf("stream mutation effect=%s, original policy=%s", after.FinalEffect, before.FinalEffect)
				}
				data, err := os.ReadFile(base)
				if operation == "create" {
					if !os.IsNotExist(err) {
						t.Fatalf("stream alias created base file: data=%q err=%v", data, err)
					}
				} else if err != nil || string(data) != original {
					t.Fatalf("stream alias modified base file: data=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestArtifactWindowsOrdinaryDriveMutationStillRuns(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive path")
	}
	ws := t.TempDir()
	path := filepath.Join(ws, "poster.png")
	tctx := &tool.Context{WorkingDirectory: ws}
	for _, operation := range []string{"create", "overwrite", "edit"} {
		t.Run(operation, func(t *testing.T) {
			if operation != "create" {
				input, _ := json.Marshal(map[string]string{"path": path})
				if _, err := (read.Tool{}).Run(context.Background(), input, tctx, nil); err != nil {
					t.Fatal(err)
				}
			}
			var mutation tool.Tool = write.Tool{}
			content := "created"
			if operation == "overwrite" {
				content = "overwritten"
			}
			input, _ := json.Marshal(map[string]string{"path": path, "content": content})
			if operation == "edit" {
				mutation, content = edit.Tool{}, "edited"
				input, _ = json.Marshal(map[string]string{"path": path, "old_string": "overwritten", "new_string": content})
			}
			action, err := (permissions.DefaultResolver{}).Resolve(context.Background(), permissions.ResolveRequest{ToolName: mutation.Name(), Input: input, Context: tctx})
			if err != nil {
				t.Fatal(err)
			}
			if d := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action); d.FinalEffect != permissions.EffectAllow {
				t.Fatalf("ordinary drive path effect=%s, want allow", d.FinalEffect)
			}
			if _, err := mutation.Run(context.Background(), input, tctx, nil); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != content {
				t.Fatalf("ordinary artifact data=%q err=%v", data, err)
			}
		})
	}
}

func TestBrowserLoopbackPreviewNoApproval(t *testing.T) {
	p := newArtifactPolicy(permissions.DefaultPolicy{})
	action := permissions.Action{ToolName: browsertool.Name, Operation: permissions.OperationExternal, Risk: permissions.RiskHigh, Resources: []permissions.Resource{{Type: permissions.ResourceExternal, Scope: permissions.ResourceScopeOutside}}, Metadata: map[string]any{metadataArtifactPreview: true}}
	if d := p.Decide(context.Background(), action); d.FinalEffect != permissions.EffectAllow || d.Reason != reasonArtifactPreview {
		t.Fatalf("bound preview => %+v, want allow", d)
	}
	action.Metadata = map[string]any{permissions.MetadataNetworkHost: "localhost:1234"}
	if d := p.Decide(context.Background(), action); d.FinalEffect != permissions.EffectAsk {
		t.Fatalf("unbound localhost => %+v, want ask", d)
	}
}

func TestArtifactPreviewNoApproval(t *testing.T) {
	action, err := (permissions.DefaultResolver{}).Resolve(context.Background(), permissions.ResolveRequest{ToolName: "open_preview"})
	if err != nil {
		t.Fatal(err)
	}
	if d := newArtifactPolicy(permissions.DefaultPolicy{}).Decide(context.Background(), action); d.FinalEffect != permissions.EffectAllow {
		t.Fatalf("open_preview => %+v, want allow", d)
	}
}

func TestBrowserResolverMarksOnlySessionPreview(t *testing.T) {
	base := "http://127.0.0.1:43210/"
	inner := permissions.WithToolClasses(nil, map[string]permissions.ToolClass{browsertool.Name: permissions.ClassMutating})
	r := browserResolver{inner: inner, preview: func(ctx context.Context, sessionID, workspace, raw string) bool {
		return sessionID == "s1" && workspace == "/ws" && raw == base+"file"
	}}
	input, _ := json.Marshal(map[string]string{"url": base + "file"})
	action, err := r.Resolve(context.Background(), permissions.ResolveRequest{ToolName: browsertool.Name, Input: input, Context: &tool.Context{SessionID: "s1", WorkingDirectory: "/ws"}})
	if err != nil || action.Metadata[metadataArtifactPreview] != true {
		t.Fatalf("preview resolve action=%+v err=%v", action, err)
	}
	input, _ = json.Marshal(map[string]string{"url": "http://127.0.0.1:43211/file"})
	action, err = r.Resolve(context.Background(), permissions.ResolveRequest{ToolName: browsertool.Name, Input: input, Context: &tool.Context{SessionID: "s1", WorkingDirectory: "/ws"}})
	if err != nil || action.Metadata[metadataArtifactPreview] == true {
		t.Fatalf("other loopback action=%+v err=%v", action, err)
	}
	if u, _ := url.Parse(base); u.Host == "" {
		t.Fatal("invalid fixture URL")
	}
}

func TestBrowserRemoteURLNotAutoAllowed(t *testing.T) {
	inner := permissions.WithToolClasses(nil, map[string]permissions.ToolClass{browsertool.Name: permissions.ClassMutating})
	r := browserResolver{inner: inner, preview: func(context.Context, string, string, string) bool { return true }}
	input, _ := json.Marshal(map[string]string{"url": "https://example.com/"})
	if _, err := r.Resolve(context.Background(), permissions.ResolveRequest{ToolName: browsertool.Name, Input: input, Context: &tool.Context{SessionID: "s1", WorkingDirectory: "/ws"}}); err == nil {
		t.Fatal("remote browser URL must not be accepted")
	}
}

func TestSessionArtifactPreviewURLRequiresLiveSessionAndBoundOrigin(t *testing.T) {
	a := &App{
		sessions: map[string]*sessionEntry{"s1": {id: "s1", workspace: `C:\\ws`}},
		previews: map[string]*previewRef{`C:\\ws`: {url: "http://127.0.0.1:43210/", refs: 1}},
	}
	if !a.sessionArtifactPreviewURL(context.Background(), "s1", `C:\\ws`, "http://127.0.0.1:43210/file") {
		t.Fatal("bound preview should be trusted")
	}
	if a.sessionArtifactPreviewURL(context.Background(), "s2", `C:\\ws`, "http://127.0.0.1:43210/file") {
		t.Fatal("unknown session should not be trusted")
	}
	if a.sessionArtifactPreviewURL(context.Background(), "s1", `C:\\ws`, "http://127.0.0.1:43211/file") {
		t.Fatal("different loopback origin should not be trusted")
	}
	if a.sessionArtifactPreviewURL(context.Background(), "s1", `C:\\ws`, "http://127.0.0.1:43210/") {
		t.Fatal("preview server root should not be trusted as an artifact")
	}
	a.sessions["s1"].closed = true
	if a.sessionArtifactPreviewURL(context.Background(), "s1", `C:\\ws`, "http://127.0.0.1:43210/file") {
		t.Fatal("closed session should not be trusted")
	}
}
