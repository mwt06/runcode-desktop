package desktop

import (
	"errors"
	"fmt"
	"io/fs"
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

// ImportSkill opens a folder picker (defaulting to ~/.claude/skills) and copies
// skills into the given scope's skills directory, each under its declared name
// with all its related files (references/, scripts/, assets/, …). The chosen
// folder may be a single skill (it holds a SKILL.md) or a container of skills
// like .claude/skills (each immediate subdirectory holding a SKILL.md is
// imported). Returns the unchanged list when the user cancels.
func (a *App) ImportSkill(scope string) (SkillList, error) {
	root, err := a.skillRoot(scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if a.dialog == nil {
		return SkillList{}, wireError(errors.New("当前环境不支持文件选择"))
	}
	src, err := a.dialog.PickFolder("选择技能文件夹，或包含多个技能的目录（如 .claude/skills）", defaultSkillsDir())
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if strings.TrimSpace(src) == "" {
		return a.ListSkills(), nil // cancelled
	}
	imported, err := importSkillsFrom(src, root)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if imported == 0 {
		return SkillList{}, wireError(errors.New("所选文件夹及其子目录都没有有效的技能（含合法 SKILL.md）"))
	}
	a.reloadSessionSkills()
	return a.ListSkills(), nil
}

// defaultSkillsDir is the conventional Claude skills directory (~/.claude/skills),
// used as the import picker's starting location. Empty if the home dir is unknown.
func defaultSkillsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".claude", "skills")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// importSkillsFrom copies skills out of src into the scope root. If src itself is
// a skill (has SKILL.md) exactly that one is imported; otherwise every immediate
// subdirectory that holds a SKILL.md is imported (so a container like
// .claude/skills imports all of its skills). Returns how many were imported.
func importSkillsFrom(src, root string) (int, error) {
	if hasSkillManifest(src) {
		if err := importOneSkill(src, root); err != nil {
			return 0, err
		}
		return 1, nil
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, fmt.Errorf("读取所选目录失败: %w", err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(src, e.Name())
		if !hasSkillManifest(child) {
			continue
		}
		if err := importOneSkill(child, root); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func hasSkillManifest(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil && !info.IsDir()
}

// importOneSkill validates a single skill folder's SKILL.md and copies the whole
// folder into root/<declared-name>.
func importOneSkill(src, root string) error {
	data, err := os.ReadFile(filepath.Join(src, "SKILL.md"))
	if err != nil {
		return fmt.Errorf("读取 %s 的 SKILL.md 失败: %w", filepath.Base(src), err)
	}
	name := frontmatterName(string(data))
	if !validSkillName(name) {
		return fmt.Errorf("%s 的 SKILL.md 缺少合法的 name", filepath.Base(src))
	}
	if err := copySkillDir(src, filepath.Join(root, name)); err != nil {
		return fmt.Errorf("导入技能文件失败: %w", err)
	}
	return nil
}

// copySkillDir recursively copies a skill folder src into dst — regular files and
// subdirectories only; symlinks and special files are skipped so an import cannot
// reach outside the chosen folder. Existing files at dst are overwritten.
func copySkillDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks / special files
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
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
