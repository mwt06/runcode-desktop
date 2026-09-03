package desktop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// testAppDirPolicy builds the wrapper over explicit roots so the assertions do
// not depend on where this machine puts its config directory.
func testAppDirPolicy(t *testing.T) (p appDirPolicy, data, install, elsewhere string) {
	t.Helper()
	base := t.TempDir()
	data = filepath.Join(base, "appdata", "runcode")
	install = filepath.Join(base, "program")
	elsewhere = filepath.Join(base, "someone-elses-project")
	p = appDirPolicy{
		inner:      permissions.DefaultPolicy{},
		readRoots:  []string{data, install},
		writeRoots: []string{data},
	}
	return p, data, install, elsewhere
}

func fileAction(op permissions.Operation, paths []string, metadata map[string]any) permissions.Action {
	resources := make([]permissions.Resource, 0, len(paths))
	for _, path := range paths {
		resources = append(resources, permissions.Resource{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeOutside, Path: path})
	}
	return permissions.Action{ToolName: "T", Operation: op, Resources: resources, Metadata: metadata}
}

func TestAppDirPolicyAllowsOwnDirectories(t *testing.T) {
	t.Parallel()
	p, data, install, _ := testAppDirPolicy(t)
	fresh := map[string]any{permissions.MetadataReadState: permissions.ReadStateFresh}

	allowed := []struct {
		name   string
		action permissions.Action
	}{
		{"read a user skill", fileAction(permissions.OperationRead, []string{filepath.Join(data, "skills", "写周报", "SKILL.md")}, nil)},
		{"read the install dir", fileAction(permissions.OperationRead, []string{filepath.Join(install, "XRUN.exe")}, nil)},
		{"write a user skill", fileAction(permissions.OperationWrite, []string{filepath.Join(data, "skills", "写周报", "SKILL.md")}, fresh)},
		{"edit an agent", fileAction(permissions.OperationEdit, []string{filepath.Join(data, "agents", "reviewer.md")}, fresh)},
		{"the data root itself", fileAction(permissions.OperationRead, []string{data}, nil)},
	}
	for _, tc := range allowed {
		if d := p.Decide(context.Background(), tc.action); d.Effect != permissions.EffectAllow || d.Reason != reasonAppDir {
			t.Errorf("%s = %#v, want allow/app_dir", tc.name, d)
		}
	}
}

func TestAppDirPolicyLeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()
	p, data, install, elsewhere := testAppDirPolicy(t)
	fresh := map[string]any{permissions.MetadataReadState: permissions.ReadStateFresh}
	skill := filepath.Join(data, "skills", "写周报", "SKILL.md")

	unchanged := []struct {
		name   string
		action permissions.Action
		reason permissions.Reason
	}{
		// 安装目录只放行读：里面躺着正在运行的可执行文件。
		{"write into the install dir", fileAction(permissions.OperationWrite, []string{filepath.Join(install, "XRUN.exe")}, fresh), permissions.ReasonOutsideWorkspace},
		{"read someone else's project", fileAction(permissions.OperationRead, []string{filepath.Join(elsewhere, "main.go")}, nil), permissions.ReasonOutsideWorkspace},
		// 一次动作只要有一个目标不在我们家，整条照旧走审批。
		{"one target of two is elsewhere", fileAction(permissions.OperationRead, []string{skill, filepath.Join(elsewhere, "main.go")}, nil), permissions.ReasonOutsideWorkspace},
		// 删除永远要人点头（工作区内也一样），那不是边界规则。
		{"delete a user skill", fileAction(permissions.OperationDelete, []string{skill}, nil), permissions.ReasonOutsideWorkspace},
		// 写前置校验是另一道闸门，不能顺手放行。
		{"overwrite a stale skill", fileAction(permissions.OperationEdit, []string{skill}, map[string]any{permissions.MetadataReadState: permissions.ReadStateStale}), permissions.ReasonReadStale},
		// 命令的资源是命令行文本，猜不出它会碰哪些文件。
		{"shell command reading outside", permissions.Action{
			ToolName:  "Bash",
			Operation: permissions.OperationExecute,
			Resources: []permissions.Resource{{Type: permissions.ResourceCommand, Scope: permissions.ResourceScopeOutside, Path: "cat " + skill}},
			Metadata:  map[string]any{permissions.MetadataCommandReadsOutside: true},
		}, permissions.ReasonOutsideWorkspace},
	}
	for _, tc := range unchanged {
		d := p.Decide(context.Background(), tc.action)
		if d.Reason != tc.reason {
			t.Errorf("%s = %#v, want reason %q untouched", tc.name, d, tc.reason)
		}
		if d.Effect == permissions.EffectAllow && d.Reason == reasonAppDir {
			t.Errorf("%s was wrongly upgraded to an app-dir allow", tc.name)
		}
	}
}

func TestAppDirPolicyPassesThroughNonBoundaryDecisions(t *testing.T) {
	t.Parallel()
	p, data, _, _ := testAppDirPolicy(t)
	// 工作区内的读本来就放行，理由不是"越界"，包装层不该插手。
	action := permissions.Action{
		ToolName:  "Read",
		Operation: permissions.OperationRead,
		Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeWorkspace, Path: filepath.Join(data, "skills", "x.md")}},
	}
	if d := p.Decide(context.Background(), action); d.Reason != permissions.ReasonAllowedRead {
		t.Fatalf("workspace read = %#v, want the inner policy's allowed_read", d)
	}
}

func TestAppDataRootsAreThisAppsOwn(t *testing.T) {
	t.Parallel()
	roots := appDataRoots()
	if len(roots) == 0 {
		t.Skip("no OS config/cache dir on this machine")
	}
	for _, root := range roots {
		if filepath.Base(root) != "runcode" {
			t.Errorf("app data root %q does not end in runcode — it would hand out standing access to a shared directory", root)
		}
		if !filepath.IsAbs(root) {
			t.Errorf("app data root %q is not absolute", root)
		}
	}
	if strings.TrimSpace(appInstallRoot()) == "" {
		t.Error("install root is empty — os.Executable failed")
	}
}
