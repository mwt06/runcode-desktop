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

// ListSkills loads both user (global) and project (workspace) skills, matching the
// precedence the AI sees (user shadows a same-named project skill). Edits apply to
// the running session immediately via ReloadSkills.
func (a *App) ListSkills() SkillList {
	var roots []skill.Root
	if user := userResourceDir(kindSkills); user != "" {
		roots = append(roots, skill.Root{Dir: user, Source: skill.SourceUser})
	}
	if project := a.projectResourceDir(kindSkills); project != "" {
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
		displayName, displayDesc := skillDisplayMeta(sk.Path)
		out.Skills = append(out.Skills, SkillInfo{
			Name:               sk.Name,
			DisplayName:        displayName,
			DisplayDescription: displayDesc,
			Description:        sk.Description,
			Body:               sk.Body,
			Source:             string(sk.Source),
			Path:               sk.Path,
			Editable:           true,
			DisabledUser:       userSkills[sk.Name],
			DisabledProject:    projSkills[sk.Name],
		})
	}
	for _, p := range problems {
		out.Problems = append(out.Problems, SkillProblem{Dir: p.Dir, Reason: p.Reason})
	}
	return out
}

// skillDisplayNameKeys / skillDisplayDescKeys are the frontmatter spellings the
// two human-facing fields may arrive under. The market's API fields are
// display_name / description; a hand-written SKILL.md is likelier to follow the
// allowed-tools style and use hyphens. Each set is one datum, not several.
var (
	skillDisplayNameKeys = []string{"display-name", "display_name", "displayname"}
	skillDisplayDescKeys = []string{"display-description", "display_description", "displaydescription"}
)

// skillDisplayMeta reads a skill's human-facing name and description out of its
// SKILL.md frontmatter; either is "" when absent (callers fall back to the
// skill's own name / description).
//
// Why a *second* description rather than reusing the frontmatter one: the two
// serve different readers. The frontmatter `description` is part of the prompt —
// it is how the model decides whether to load this skill, which is why market
// packages phrase it "Use when normalizing academic references, converting…".
// That sentence is a decision rule, and it reads terribly as a catalog blurb in
// a Chinese UI. Overwriting it with the catalog's copy would fix the list and
// quietly degrade skill triggering, so the two sit side by side: the model keeps
// its rule, the list gets its blurb.
//
// Read from the file rather than carried by the engine's loader: the engine reads
// only name/description and drops every other frontmatter key — deliberately.
// These are for people, not extra vocabulary for the model. So the shell picks
// them up on its own.
func skillDisplayMeta(path string) (name, desc string) {
	if path == "" {
		return "", ""
	}
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the skill loader, not from user input
	if err != nil {
		return "", ""
	}
	content := string(data)
	return strings.TrimSpace(frontmatterValue(content, skillDisplayNameKeys...)),
		strings.TrimSpace(frontmatterValue(content, skillDisplayDescKeys...))
}

// setSkillDisplayMeta writes the market's display name and description into
// dir/SKILL.md's frontmatter, skipping either one the package already declares
// (what the author shipped in the file beats a catalog row) and either one that
// is empty. A no-op on a file without frontmatter.
//
// Why persist at all: once installed, a skill has no link back to the market —
// the plugins page reads the local directory. Keeping these only in the cached
// market listing would mean they vanish the moment that cache is dropped.
// Frontmatter is the skill's own metadata, and extra keys there cost nothing:
// the loader ignores the ones it does not know.
func setSkillDisplayMeta(dir, name, desc string) error {
	path := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(path) //nolint:gosec // dir is the staging directory this package just created
	if err != nil {
		return err
	}
	content := string(data)
	add := make([]string, 0, 2)
	for _, f := range []struct {
		key   string
		value string
		seen  []string
	}{
		{"display-name", collapseLine(name), skillDisplayNameKeys},
		{"display-description", collapseLine(desc), skillDisplayDescKeys},
	} {
		if f.value != "" && strings.TrimSpace(frontmatterValue(content, f.seen...)) == "" {
			add = append(add, f.key+": "+f.value)
		}
	}
	if len(add) == 0 {
		return nil
	}
	bom := ""
	if rest, ok := strings.CutPrefix(content, string(utf8BOM)); ok {
		bom, content = string(utf8BOM), rest
	}
	// Insert right after the opening "---" so the lines land inside the block.
	// Keep the file's own line endings out of it: writing "\n" into a CRLF file
	// is fine for a line-based parser, and rewriting the whole file's endings
	// would touch content this function has no business changing.
	first, rest, ok := strings.Cut(content, "\n")
	if !ok || strings.TrimSpace(strings.TrimSuffix(first, "\r")) != "---" {
		return nil // no frontmatter to extend; leave the file alone
	}
	out := bom + first + "\n" + strings.Join(add, "\n") + "\n" + rest
	return os.WriteFile(path, []byte(out), 0o600)
}

// installedSkillNames lists what is installed under one skills root: immediate
// subdirectories holding a SKILL.md.
//
// Deliberately not derived from ListSkills. That list is the view the model gets,
// where a user skill shadows a same-named project one — so a project skill that
// really is sitting on disk would read as "not installed" and lose its uninstall
// entry. "Is it on disk" is a question for the disk.
func installedSkillNames(root string) map[string]bool {
	out := map[string]bool{}
	if root == "" {
		return out
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && hasSkillManifest(filepath.Join(root, e.Name())) {
			out[e.Name()] = true
		}
	}
	return out
}

// SaveSkill writes a skill's SKILL.md to its scope's root and returns the list.
func (a *App) SaveSkill(req SkillSaveRequest) (SkillList, error) {
	root, err := a.resourceRoot(kindSkills, req.Scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	name := strings.TrimSpace(req.Name)
	if !validResourceName(name) {
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
	// The two display-* keys only appear when there is something to put in them:
	// an empty key would read as "its display name IS the empty string", and every
	// fallback ("no display name → show the id") would have to special-case it.
	var fm strings.Builder
	fmt.Fprintf(&fm, "---\nname: %s\n", name)
	if d := collapseLine(req.DisplayName); d != "" {
		fmt.Fprintf(&fm, "display-name: %s\n", d)
	}
	if d := collapseLine(req.DisplayDescription); d != "" {
		fmt.Fprintf(&fm, "display-description: %s\n", d)
	}
	fmt.Fprintf(&fm, "description: %s\n---\n\n%s\n", description, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fm.String()), 0o600); err != nil {
		return SkillList{}, wireError(fmt.Errorf("write SKILL.md: %w", err))
	}
	// On rename, drop the old directory.
	if old := strings.TrimSpace(req.OriginalName); old != "" && old != name && validResourceName(old) {
		_ = os.RemoveAll(filepath.Join(root, old))
	}
	a.reloadSessionSkills()
	return a.ListSkills(), nil
}

// DeleteSkill removes a skill directory in the given scope and returns the list.
func (a *App) DeleteSkill(name, scope string) (SkillList, error) {
	root, err := a.resourceRoot(kindSkills, scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	name = strings.TrimSpace(name)
	if !validResourceName(name) {
		return SkillList{}, wireError(errors.New("无效的技能名"))
	}
	if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
		return SkillList{}, wireError(fmt.Errorf("delete skill: %w", err))
	}
	// 内置技能删了会在下次启动被装回来（见 installBuiltinSkills：磁盘上缺了就补）。
	// 所以这里把用户的意图落到一个重装吃不掉的地方——停用。否则用户删了它，下次开
	// 应用它又回来了，而且还是启用的，等于这个删除按钮在骗人。
	if isBuiltinSkill(name) {
		if err := a.setDisabled(scope, "skill", false, name); err != nil {
			debugLog("disable builtin skill %s after delete: %v", name, err)
		}
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
	root, err := a.resourceRoot(kindSkills, scope)
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
	if !validResourceName(name) {
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

// reloadSessionSkills makes skill edits take effect in the running session, so a
// newly created/edited skill is usable immediately without a new conversation.
func (a *App) reloadSessionSkills() {
	if session, err := a.engineSession(); err == nil {
		session.ReloadSkills()
	}
}
