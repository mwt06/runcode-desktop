package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
)

// isBuiltinAgentName reports whether name is one of the compiled-in agents, which
// are read-only: they can be shadowed by a same-named user/project agent but
// never edited or deleted in place.
func isBuiltinAgentName(name string) bool {
	for _, ag := range engine.BuiltinAgents() {
		if ag.Name == name {
			return true
		}
	}
	return false
}

// ListAgents loads built-in, user, and project sub-agents (the same set the AI
// sees), so the manager mirrors the catalog. Built-ins are read-only.
func (a *App) ListAgents() AgentList {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	userCfg, _ := os.UserConfigDir()
	set, problems := engine.LoadAgents(ws, userCfg)
	uc, pc := scopeDisabled(ws)
	userAgents, projAgents := toStringSet(uc.Agents), toStringSet(pc.Agents)
	out := AgentList{Agents: []AgentInfo{}, Problems: []AgentProblem{}}
	for _, ag := range set.All() {
		out.Agents = append(out.Agents, AgentInfo{
			Name:            ag.Name,
			Description:     ag.Description,
			Tools:           strings.Join(ag.Tools, ", "),
			Model:           ag.Model,
			Prompt:          ag.Prompt,
			Source:          string(ag.Source),
			Path:            ag.Path,
			Editable:        ag.Path != "", // builtins have no file and cannot be edited here
			DisabledUser:    userAgents[ag.Name],
			DisabledProject: projAgents[ag.Name],
		})
	}
	for _, p := range problems {
		out.Problems = append(out.Problems, AgentProblem{Path: p.Path, Reason: p.Reason})
	}
	return out
}

// SaveAgent writes a sub-agent's <name>.md to its scope's root and returns the list.
func (a *App) SaveAgent(req AgentSaveRequest) (AgentList, error) {
	root, err := a.resourceRoot(kindAgents, req.Scope)
	if err != nil {
		return AgentList{}, wireError(err)
	}
	name := strings.TrimSpace(req.Name)
	if !validResourceName(name) {
		return AgentList{}, wireError(errors.New("子代理名只能包含字母、数字、- 或 _，且不超过 64 个字符"))
	}
	// 内置子代理不可就地修改（编辑/重命名内置本身）。允许在用户/项目级新建同名
	// 子代理来覆盖它——那是 originalName 为空的新建路径，不受此拦截影响。
	if old := strings.TrimSpace(req.OriginalName); old != "" && isBuiltinAgentName(old) {
		return AgentList{}, wireError(errors.New("内置子代理不可修改；如需自定义，请在用户/项目级新建同名子代理来覆盖它"))
	}
	description := strings.TrimSpace(collapseLine(req.Description))
	if description == "" {
		return AgentList{}, wireError(errors.New("子代理描述不能为空"))
	}
	prompt := strings.TrimRight(req.Prompt, "\n")
	if strings.TrimSpace(prompt) == "" {
		return AgentList{}, wireError(errors.New("子代理指令正文不能为空"))
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return AgentList{}, wireError(fmt.Errorf("create agents dir: %w", err))
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", description)
	if tools := strings.TrimSpace(collapseLine(req.Tools)); tools != "" {
		fmt.Fprintf(&b, "tools: %s\n", tools)
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		fmt.Fprintf(&b, "model: %s\n", model)
	}
	b.WriteString("---\n\n")
	b.WriteString(prompt)
	b.WriteString("\n")
	if err := os.WriteFile(filepath.Join(root, name+".md"), []byte(b.String()), 0o600); err != nil {
		return AgentList{}, wireError(fmt.Errorf("write agent file: %w", err))
	}
	// On rename, drop the old file.
	if old := strings.TrimSpace(req.OriginalName); old != "" && old != name && validResourceName(old) {
		_ = os.Remove(filepath.Join(root, old+".md"))
	}
	a.reloadSessionAgents()
	return a.ListAgents(), nil
}

// DeleteAgent removes a sub-agent file in the given scope and returns the list.
func (a *App) DeleteAgent(name, scope string) (AgentList, error) {
	root, err := a.resourceRoot(kindAgents, scope)
	if err != nil {
		return AgentList{}, wireError(err)
	}
	name = strings.TrimSpace(name)
	if !validResourceName(name) {
		return AgentList{}, wireError(errors.New("无效的子代理名"))
	}
	if isBuiltinAgentName(name) {
		return AgentList{}, wireError(errors.New("内置子代理不可删除"))
	}
	if err := os.Remove(filepath.Join(root, name+".md")); err != nil && !os.IsNotExist(err) {
		return AgentList{}, wireError(fmt.Errorf("delete agent: %w", err))
	}
	a.reloadSessionAgents()
	return a.ListAgents(), nil
}

// ImportAgent opens a file picker for an existing agent .md and copies it into the
// given scope's agents directory under its declared name. Returns the unchanged
// list when the user cancels.
func (a *App) ImportAgent(scope string) (AgentList, error) {
	root, err := a.resourceRoot(kindAgents, scope)
	if err != nil {
		return AgentList{}, wireError(err)
	}
	if a.dialog == nil {
		return AgentList{}, wireError(errors.New("当前环境不支持文件选择"))
	}
	path, err := a.dialog.PickFile("选择要导入的子代理 .md")
	if err != nil {
		return AgentList{}, wireError(err)
	}
	if strings.TrimSpace(path) == "" {
		return a.ListAgents(), nil // cancelled
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentList{}, wireError(fmt.Errorf("读取所选文件失败: %w", err))
	}
	name := frontmatterName(string(data))
	if !validResourceName(name) {
		return AgentList{}, wireError(errors.New("所选文件不是有效的子代理定义(frontmatter 缺少合法的 name)"))
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return AgentList{}, wireError(fmt.Errorf("创建子代理目录失败: %w", err))
	}
	if err := os.WriteFile(filepath.Join(root, name+".md"), data, 0o600); err != nil {
		return AgentList{}, wireError(fmt.Errorf("写入子代理文件失败: %w", err))
	}
	a.reloadSessionAgents()
	return a.ListAgents(), nil
}

// reloadSessionAgents makes sub-agent edits take effect in the running session.
func (a *App) reloadSessionAgents() {
	if session, err := a.engineSession(); err == nil {
		session.ReloadAgents()
	}
}
