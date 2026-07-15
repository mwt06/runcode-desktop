package desktop

import (
	"testing"
)

func TestSetToolEnabledUserScopePersistsAndReflects(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	// 关闭 Bash(用户级) → 有效关闭名单包含 Bash，工具页对应行标记 disabledUser。
	if err := app.SetToolEnabled("Bash", "user", false); err != nil {
		t.Fatalf("disable Bash: %v", err)
	}
	tools, _, _ := effectiveDisabled("")
	if !contains(tools, "Bash") {
		t.Fatalf("effective disabled tools = %v, want Bash", tools)
	}
	var bash *ToolInfo
	for i := range app.ListTools() {
		if it := app.ListTools()[i]; it.Name == "Bash" {
			bash = &it
			break
		}
	}
	if bash == nil {
		t.Fatal("Bash must still appear in ListTools (so it can be re-enabled)")
	}
	if !bash.DisabledUser || bash.DisabledProject {
		t.Fatalf("Bash flags = user:%v project:%v, want user-only", bash.DisabledUser, bash.DisabledProject)
	}

	// 重新启用 → 名单清空。
	if err := app.SetToolEnabled("Bash", "user", true); err != nil {
		t.Fatalf("enable Bash: %v", err)
	}
	if tools, _, _ = effectiveDisabled(""); contains(tools, "Bash") {
		t.Fatalf("Bash still disabled after enable: %v", tools)
	}
}

func TestBuildConfigCarriesDisabledSets(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	if err := app.SetToolEnabled("WebSearch", "user", false); err != nil {
		t.Fatal(err)
	}
	if err := app.SetAgentEnabled("debugger", "user", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(StartSessionRequest{CWD: t.TempDir(), Provider: "openai", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(cfg.DisabledTools, "WebSearch") {
		t.Fatalf("cfg.DisabledTools = %v, want WebSearch", cfg.DisabledTools)
	}
	if !contains(cfg.DisabledAgents, "debugger") {
		t.Fatalf("cfg.DisabledAgents = %v, want debugger", cfg.DisabledAgents)
	}
}

func TestProjectScopeToggleRequiresWorkspace(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{}) // no workspace set
	if err := app.SetToolEnabled("Bash", "project", false); err == nil {
		t.Fatal("project-scope toggle without a workspace must error")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
