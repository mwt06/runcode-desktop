package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/skill"
)

// projectSkillsDir is the workspace directory that holds project skills.
func (a *App) projectSkillsDir() string {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" {
		return ""
	}
	return filepath.Join(ws, ".runcode", "skills")
}

// userSkillsDir is the per-user (global) skills directory.
func userSkillsDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "runcode", "skills")
}

// skillRoot resolves the directory for a scope ("project" or "user").
func (a *App) skillRoot(scope string) (string, error) {
	switch scope {
	case "user":
		root := userSkillsDir()
		if root == "" {
			return "", errors.New("无法定位用户配置目录")
		}
		return root, nil
	default: // project
		root := a.projectSkillsDir()
		if root == "" {
			return "", errNoSession
		}
		return root, nil
	}
}

// ListSkills loads both user (global) and project (workspace) skills, matching the
// precedence the AI sees (user shadows a same-named project skill). Edits apply to
// the running session immediately via ReloadSkills.
func (a *App) ListSkills() SkillList {
	var roots []skill.Root
	if user := userSkillsDir(); user != "" {
		roots = append(roots, skill.Root{Dir: user, Source: skill.SourceUser})
	}
	if project := a.projectSkillsDir(); project != "" {
		roots = append(roots, skill.Root{Dir: project, Source: skill.SourceProject})
	}
	set, problems := skill.Load(skill.LoadOptions{Roots: roots})
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	uc, pc := scopeDisabled(ws)
	userSkills, projSkills := toStringSet(uc.Skills), toStringSet(pc.Skills)
	out := SkillList{Skills: []SkillInfo{}, Problems: []SkillProblem{}}
	for _, sk := range set.All() {
		out.Skills = append(out.Skills, SkillInfo{
			Name:            sk.Name,
			Description:     sk.Description,
			Body:            sk.Body,
			Source:          string(sk.Source),
			Path:            sk.Path,
			Editable:        true,
			DisabledUser:    userSkills[sk.Name],
			DisabledProject: projSkills[sk.Name],
		})
	}
	for _, p := range problems {
		out.Problems = append(out.Problems, SkillProblem{Dir: p.Dir, Reason: p.Reason})
	}
	return out
}

// SaveSkill writes a skill's SKILL.md to its scope's root and returns the list.
func (a *App) SaveSkill(req SkillSaveRequest) (SkillList, error) {
	root, err := a.skillRoot(req.Scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	name := strings.TrimSpace(req.Name)
	if !validSkillName(name) {
		return SkillList{}, wireError(errors.New("技能名只能包含字母、数字、- 或 _，且不超过 64 个字符"))
	}
	description := strings.TrimSpace(collapseLine(req.Description))
	if description == "" {
		return SkillList{}, wireError(errors.New("技能描述不能为空"))
	}
	body := strings.TrimRight(req.Body, "\n")

	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SkillList{}, wireError(fmt.Errorf("create skill dir: %w", err))
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, description, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		return SkillList{}, wireError(fmt.Errorf("write SKILL.md: %w", err))
	}
	// On rename, drop the old directory.
	if old := strings.TrimSpace(req.OriginalName); old != "" && old != name && validSkillName(old) {
		_ = os.RemoveAll(filepath.Join(root, old))
	}
	a.reloadSessionSkills()
	return a.ListSkills(), nil
}

// DeleteSkill removes a skill directory in the given scope and returns the list.
func (a *App) DeleteSkill(name, scope string) (SkillList, error) {
	root, err := a.skillRoot(scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	name = strings.TrimSpace(name)
	if !validSkillName(name) {
		return SkillList{}, wireError(errors.New("无效的技能名"))
	}
	if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
		return SkillList{}, wireError(fmt.Errorf("delete skill: %w", err))
	}
	a.reloadSessionSkills()
	return a.ListSkills(), nil
}

// ImportSkill opens a file picker for an existing SKILL.md and copies it into the
// given scope's skills directory under its declared name. Returns the unchanged
// list when the user cancels.
func (a *App) ImportSkill(scope string) (SkillList, error) {
	root, err := a.skillRoot(scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if a.dialog == nil {
		return SkillList{}, wireError(errors.New("当前环境不支持文件选择"))
	}
	path, err := a.dialog.PickFile("选择要导入的 SKILL.md")
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if strings.TrimSpace(path) == "" {
		return a.ListSkills(), nil // cancelled
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillList{}, wireError(fmt.Errorf("读取所选文件失败: %w", err))
	}
	name := frontmatterName(string(data))
	if !validSkillName(name) {
		return SkillList{}, wireError(errors.New("所选文件不是有效的 SKILL.md(frontmatter 缺少合法的 name)"))
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SkillList{}, wireError(fmt.Errorf("创建技能目录失败: %w", err))
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), data, 0o600); err != nil {
		return SkillList{}, wireError(fmt.Errorf("写入 SKILL.md 失败: %w", err))
	}
	a.reloadSessionSkills()
	return a.ListSkills(), nil
}

// frontmatterName extracts the name from a SKILL.md's "--- ... ---" frontmatter.
func frontmatterName(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			break
		}
		key, value, ok := strings.Cut(lines[i], ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "name") {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// reloadSessionSkills makes skill edits take effect in the running session, so a
// newly created/edited skill is usable immediately without a new conversation.
func (a *App) reloadSessionSkills() {
	if session, err := a.engineSession(); err == nil {
		session.ReloadSkills()
	}
}

// validSkillName mirrors the skill package's name rule.
func validSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// collapseLine flattens a value to a single line so it stays valid in the SKILL.md
// frontmatter (which is line-based).
func collapseLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}
