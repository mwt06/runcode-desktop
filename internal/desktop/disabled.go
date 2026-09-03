package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// disabledMu serializes setDisabled's load→mutate→save cycle (both scope files).
// The toggles are Wails-bound and the frontend can fire several in quick
// succession; unsynchronized cycles would start from the same stale snapshot and
// the later save would silently drop the earlier toggle.
var disabledMu sync.Mutex

// 工具/子代理的"关闭"清单，分两个作用域持久化：
//   - 用户级(全局)：%AppData%/runcode/disabled.json，对所有工作区生效
//   - 工作目录级(项目)：<workspace>/.runcode/disabled.json，仅对该工作区生效
// 任一作用域关闭即视为关闭(取并集)。关闭的工具/子代理不会进入会话、不传给模型。
// 关闭在"下次新建会话"生效(与连接设置一致)。

// disabledConfig 是单个作用域的关闭清单；文件缺失 = 未关闭任何项。
type disabledConfig struct {
	Tools  []string `json:"tools"`
	Agents []string `json:"agents"`
	Skills []string `json:"skills"`
}

func userDisabledPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runcode", "disabled.json"), nil
}

func projectDisabledPath(workspace string) string {
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, ".runcode", "disabled.json")
}

func loadDisabled(path string) disabledConfig {
	var c disabledConfig
	if path == "" {
		return c
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(data, &c)
	return c
}

func saveDisabled(path string, c disabledConfig) error {
	if path == "" {
		return errors.New("无效的保存路径")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Atomic replace: a crash mid-write must not truncate the list and silently
	// re-enable everything the user turned off.
	return writeFileAtomic(filepath.Dir(path), filepath.Base(path), data)
}

// effectiveDisabled 返回某工作区下用户级 ∪ 项目级的关闭名单(工具、子代理、技能)。
func effectiveDisabled(workspace string) (tools, agents, skills []string) {
	uc, pc := scopeDisabled(workspace)
	return unionStrings(uc.Tools, pc.Tools), unionStrings(uc.Agents, pc.Agents), unionStrings(uc.Skills, pc.Skills)
}

// scopeDisabled 返回某工作区下用户级、项目级各自的关闭清单(供 UI 显示每个作用域
// 的状态)。
func scopeDisabled(workspace string) (user, project disabledConfig) {
	if p, err := userDisabledPath(); err == nil {
		user = loadDisabled(p)
	}
	project = loadDisabled(projectDisabledPath(workspace))
	return user, project
}

func toStringSet(list []string) map[string]bool {
	set := make(map[string]bool, len(list))
	for _, s := range list {
		set[s] = true
	}
	return set
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func addStringUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// disabledScopePath 解析作用域("user"/"project")对应的关闭文件路径。project 需要
// 有活动工作区。
func (a *App) disabledScopePath(scope string) (string, error) {
	switch scope {
	case "user":
		return userDisabledPath()
	case "project":
		a.mu.Lock()
		ws := a.workspace
		a.mu.Unlock()
		p := projectDisabledPath(ws)
		if p == "" {
			return "", errors.New("请先打开一个工作区再按工作目录级关闭")
		}
		return p, nil
	default:
		return "", errors.New("未知的作用域(应为 user 或 project)")
	}
}

// setDisabled 在指定作用域开关一个工具/子代理/技能(kind)，落盘后刷新内存中的
// 会话配置，使下次新建/恢复会话生效。
func (a *App) setDisabled(scope, kind string, enabled bool, name string) error {
	path, err := a.disabledScopePath(scope)
	if err != nil {
		return err
	}
	disabledMu.Lock()
	defer disabledMu.Unlock()
	c := loadDisabled(path)
	var target *[]string
	switch kind {
	case "tool":
		target = &c.Tools
	case "agent":
		target = &c.Agents
	case "skill":
		target = &c.Skills
	default:
		return errors.New("未知的类型")
	}
	if enabled {
		*target = removeString(*target, name)
	} else {
		*target = addStringUnique(*target, name)
	}
	if err := saveDisabled(path, c); err != nil {
		return err
	}
	a.refreshDisabledInConfig()
	return nil
}

// refreshDisabledInConfig 把当前工作区的有效关闭名单同步进 a.config，让复用
// a.config 的 NewSession/ResumeSession/OpenSession 立即采用新名单。
func (a *App) refreshDisabledInConfig() {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	t, ag, sk := effectiveDisabled(ws)
	a.mu.Lock()
	a.config.DisabledTools = t
	a.config.DisabledAgents = ag
	a.config.DisabledSkills = sk
	a.mu.Unlock()
}

// SetToolEnabled 在 scope("user"/"project")开关一个工具。下次新建会话生效。
func (a *App) SetToolEnabled(name, scope string, enabled bool) error {
	return wireError(a.setDisabled(scope, "tool", enabled, name))
}

// SetAgentEnabled 在 scope("user"/"project")开关一个子代理。下次新建会话生效。
func (a *App) SetAgentEnabled(name, scope string, enabled bool) error {
	return wireError(a.setDisabled(scope, "agent", enabled, name))
}

// SetSkillEnabled 在 scope("user"/"project")开关一个技能。下次新建会话生效。
func (a *App) SetSkillEnabled(name, scope string, enabled bool) error {
	return wireError(a.setDisabled(scope, "skill", enabled, name))
}
