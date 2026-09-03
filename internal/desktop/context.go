package desktop

import (
	"errors"
	"os"
	"path/filepath"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/projectctx"
)

// maxDocBytes bounds how much of a project-instructions file we read into the
// editor, matching the loader's generous cap for real docs.
const maxDocBytes = 256 * 1024

// defaultProjectContextName 是「这个工作区还没有项目指令」时新建的文件名。
//
// 必须是引擎 projectctx.candidateNames 里的一个，否则存下去的文件模型永远读不到——
// 编辑器写 A、加载器找 B 是那种界面上一切正常、只有模型行为不对的故障。
// TestDefaultProjectContextNameIsDiscoverable 盯着这层一致性。
const defaultProjectContextName = "AGENT.md"

// ReadProjectContext returns the workspace's project-instructions file for the
// editor. When none exists yet it reports the path AGENT.md would take, so a save
// creates it under that name.
//
// 已经存在的文件按引擎的发现顺序原样打开（RUNCODE.md → AGENT.md → CLAUDE.md），
// 不改名：仓库里那份 CLAUDE.md 是用户自己的文件，还可能被别的工具读，替他重命名
// 不是这个编辑器该做的事。新建才用新名字。
func (a *App) ReadProjectContext() (ProjectContextInfo, error) {
	ws := a.workspaceDir()
	if ws == "" {
		return ProjectContextInfo{Name: defaultProjectContextName}, nil
	}
	res, err := projectctx.Load(projectctx.LoadOptions{CWD: ws, MaxBytes: maxDocBytes})
	if err != nil {
		return ProjectContextInfo{}, wireError(err)
	}
	if res.Path == "" {
		return ProjectContextInfo{Path: filepath.Join(ws, defaultProjectContextName), Name: defaultProjectContextName}, nil
	}
	return ProjectContextInfo{Path: res.Path, Name: filepath.Base(res.Path), Content: res.Content, Exists: true}, nil
}

// SaveProjectContext writes the project-instructions file, targeting the same file
// ReadProjectContext surfaced (the existing RUNCODE.md/AGENT.md/CLAUDE.md, or a
// new AGENT.md in the workspace root).
func (a *App) SaveProjectContext(content string) error {
	info, err := a.ReadProjectContext()
	if err != nil {
		return err
	}
	if info.Path == "" {
		return wireError(errors.New("没有活动工作区"))
	}
	// 0644 而非 0600 是有意的:项目指令(AGENT.md / RUNCODE.md)是用户仓库里的
	// 普通源文件,要能被编辑器、git 和协作者正常读取——这不是应用私有状态。
	//nolint:gosec // G306
	return wireError(os.WriteFile(info.Path, []byte(content), 0o644))
}

// ReadMemory returns the agent's persistent memory (user + project scopes) for
// display.
func (a *App) ReadMemory() (MemoryInfo, error) {
	dir, _ := os.UserConfigDir()
	loaded, err := engine.MemoryStore(a.workspaceDir(), dir).Load()
	if err != nil {
		return MemoryInfo{}, wireError(err)
	}
	// Return non-nil slices so the JSON is [] rather than null — the frontend renders
	// a list from these directly.
	return MemoryInfo{User: orEmptySlice(loaded.User), Project: orEmptySlice(loaded.Project)}, nil
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *App) workspaceDir() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workspace
}
