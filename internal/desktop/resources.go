package desktop

// 技能(skills)与子代理(agents)是同构的"可管理资源":都放在两个作用域下的同名
// 目录里——
//
//	项目级  <workspace>/.runcode/<kind>
//	用户级  <os.UserConfigDir>/runcode/<kind>
//
// 目录解析、命名规则与 frontmatter 处理只在这里定义一次,skills.go 与 agents.go
// 共用;两边原先各写一份、改一处就要记得改另一处。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// resourceKind is the directory name a resource lives under in either scope.
type resourceKind string

const (
	kindSkills resourceKind = "skills"
	kindAgents resourceKind = "agents"
)

// projectResourceDir is the workspace directory holding a kind's project-level
// resources; "" when there is no active session (and therefore no workspace).
func (a *App) projectResourceDir(kind resourceKind) string {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" {
		return ""
	}
	return filepath.Join(ws, ".runcode", string(kind))
}

// userResourceDir is the per-user (global) directory for a kind; "" when the OS
// config directory cannot be located.
func userResourceDir(kind resourceKind) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "runcode", string(kind))
}

// resourceRoot resolves the directory for a scope ("project" or "user"). Any
// value other than "user" is treated as project scope, matching the frontend's
// two-way scope switch.
func (a *App) resourceRoot(kind resourceKind, scope string) (string, error) {
	switch scope {
	case "user":
		root := userResourceDir(kind)
		if root == "" {
			return "", errors.New("无法定位用户配置目录")
		}
		return root, nil
	default: // project
		root := a.projectResourceDir(kind)
		if root == "" {
			return "", errNoSession
		}
		return root, nil
	}
}

// validResourceName mirrors the skill package's name rule. Sub-agents share it so
// a name is valid in exactly one way across both kinds — and so neither can write
// a file outside its root (no separators, no dots).
func validResourceName(name string) bool {
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

// collapseLine flattens a value to a single line so it stays valid in the
// frontmatter (which is line-based).
func collapseLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// frontmatterName extracts the name from a "--- ... ---" frontmatter block, as
// used by both SKILL.md and a sub-agent's .md.
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
