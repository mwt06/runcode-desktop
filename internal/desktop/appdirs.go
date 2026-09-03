package desktop

// 本应用自己的目录——安装目录，以及数据目录（技能、子代理、MCP 配置、通行证、
// 录音、日志……）——都落在工作区之外，于是每次读写都要走一遍"项目外授权"弹窗。
// 那条规则拦的是"agent 伸手去动别的项目"，而这两处是应用自己的家，不是别人的项目。
//
// 做法是包一层 engine 给的 IoC 端口 permissions.Policy，只把"越界"这一种裁决升级
// 成放行，其余一律原样透传。三条边界写在这里而不是引擎里：**哪些目录是"本应用
// 的"，只有外壳知道**——引擎不知道 XRUN 装在哪、配置写到哪去了。
//
//  1. 只改 ReasonOutsideWorkspace 这一种裁决。写前置校验（read_stale /
//     write_exists）、删除确认、危险命令硬拒统统不碰——那些不是边界规则，是别的
//     规则，顺手放行等于偷偷拆掉三道无关的闸门。
//  2. 安装目录只放行读。里面躺着正在运行的可执行文件，静默覆盖它的后果比省一次
//     弹窗严重得多。数据目录才是读写全开的那个——用户要改的技能就在那儿。
//  3. 只认工具解析出来的绝对路径资源（Read / Glob / Grep / Write / Edit）。shell
//     命令的资源是命令行文本，从字符串猜它到底会碰哪些文件不可靠，所以命令照旧
//     走审批。
//
// 放行之后工具那一侧照样过得去，不需要另外开口子：executor 在授权之后按 action
// 是否越界去置 tool.Context 的 OutsideAllowed，工具自己的边界守卫读的就是它。

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

// reasonAppDir marks an action allowed because its target is this app's own
// directory. Reason is an open string type, and a host that supplies its own
// Policy necessarily coins its own reasons; this one never reaches the approval
// UI (an allow raises no prompt) — it is for telemetry and for reading logs.
const reasonAppDir permissions.Reason = "app_dir"

// appDataRoots are the directories this app stores its own data under. Both OS
// bases are taken because the app genuinely uses both: UserConfigDir holds the
// configuration data (skills, agents, mcp, passport, desktop.json), and on Windows the
// recorder deliberately writes to UserCacheDir instead — Roaming would sync a
// 230 MB recording across a domain profile (see defaultRecorderRoot).
//
// A user-configured recorder root is *not* included: it can be any directory the
// user picked (D:\录音, or a drive root), and standing write access to an
// arbitrary path is not something a default should hand out.
func appDataRoots() []string {
	var roots []string
	for _, base := range []func() (string, error){os.UserConfigDir, os.UserCacheDir} {
		dir, err := base()
		if err != nil || dir == "" {
			continue
		}
		roots = appendRoot(roots, filepath.Join(dir, "runcode"))
	}
	return roots
}

// appInstallRoot is the directory the running executable sits in, or "" when it
// cannot be located. Symlinks are resolved so the root is compared in the same
// terms IsWithinResolved will compare a target in.
func appInstallRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// appendRoot adds root unless it is empty or already present (the OS bases can
// coincide on some systems).
func appendRoot(roots []string, root string) []string {
	if root == "" || slices.Contains(roots, root) {
		return roots
	}
	return append(roots, root)
}

// appDirPolicy is the wrapper: inner decides, and only an "outside the
// workspace" verdict on a target under one of our own roots is upgraded.
type appDirPolicy struct {
	inner permissions.Policy
	// readRoots are the roots readable without a prompt (data + install);
	// writeRoots are the ones also writable (data only).
	readRoots  []string
	writeRoots []string
}

// appDirsOnce resolves the roots once per process: os.Executable and the OS
// config/cache dirs do not change while the app runs, and every tool call would
// otherwise re-derive them.
var appDirsOnce = sync.OnceValue(func() appDirPolicy {
	data := appDataRoots()
	read := appendRoot(append([]string{}, data...), appInstallRoot())
	return appDirPolicy{readRoots: read, writeRoots: data}
})

// newAppDirPolicy wraps inner so this app's own directories stop prompting.
func newAppDirPolicy(inner permissions.Policy) appDirPolicy {
	p := appDirsOnce()
	p.inner = inner
	return p
}

func (p appDirPolicy) Decide(ctx context.Context, action permissions.Action) permissions.Decision {
	decision := p.inner.Decide(ctx, action)
	// 只有"越界"这一条是我们要改的判定；其余（含 read_stale / write_exists 这类
	// 写前置校验的硬拒）原样透传。
	if decision.Reason != permissions.ReasonOutsideWorkspace {
		return decision
	}
	switch action.Operation {
	case permissions.OperationRead:
		if p.covers(action.Resources, p.readRoots) {
			return permissions.Allow(reasonAppDir, "desktop.app_dir.read")
		}
	case permissions.OperationWrite, permissions.OperationEdit:
		if p.covers(action.Resources, p.writeRoots) {
			return permissions.Allow(reasonAppDir, "desktop.app_dir.mutate")
		}
	}
	return decision
}

// covers reports whether every resource of the action is a path under one of
// roots. Every one of them: an action that also touches something elsewhere is
// not this app's own business and keeps asking. A resource that is not a path
// (a command line, a network host) disqualifies the action outright.
func (p appDirPolicy) covers(resources []permissions.Resource, roots []string) bool {
	if len(resources) == 0 || len(roots) == 0 {
		return false
	}
	for _, resource := range resources {
		if resource.Type != permissions.ResourceFile && resource.Type != permissions.ResourceDirectory {
			return false
		}
		if !withinAny(roots, resource.Path) {
			return false
		}
	}
	return true
}

// withinAny reports whether path resolves inside one of roots. It uses the
// engine's own containment check so "inside" means here exactly what it means at
// the workspace boundary — symlinks resolved on both sides, which is also what
// stops a link planted under our root from pointing standing access somewhere
// else entirely.
func withinAny(roots []string, path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	for _, root := range roots {
		within, err := toolpath.IsWithinResolved(root, path)
		if err == nil && within {
			return true
		}
	}
	return false
}
