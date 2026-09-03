package desktop

// 多会话的命令面：同时开着几条会话、在它们之间切换、单独关掉其中一条。
//
// 与 sessions.go 里那批的区别是**策略**：StartSession / NewSession / ResumeSession
// 是"替换式打开"——先关掉当前那条再开新的；这里是"加开一条"，已经开着的一条都
// 不动。引擎（host.Manager）本来就支持多会话，挡住的一直是外壳这层策略。

import (
	"errors"
	"sort"
	"strings"
)

// OpenSession **再开**一条会话，不关掉已经开着的那些。
//
// workspace 为空是"就在当前工作区再开一条"；给了目录就在那个目录开——这是
// **多工作区并行**的唯一入口。每条会话记着自己的目录（sessionEntry.workspace），
// 工作区寻址的那批命令跟着聚焦走（见 focusLocked），预览服务器按目录共享并计数
// （见 previewRef）。
//
// 它同时取代了原来的 SwitchWorkspace（关掉当前那条、在新目录重开一条）：换目录
// 不再意味着丢掉手上正在跑的会话。真要丢，从「打开中」里关掉它——那是一个明确的
// 动作，而不是换目录的副作用。
//
// 复用当前会话的 provider / 模型 / 凭据（与 NewSession 同一套 a.config），所以
// 并行的几条会话共用同一条连接——每会话独立连接不在本期范围内，见
// docs/multi-session.md。模型倒是每会话可以不同：SetModel 本来就按会话生效。
func (a *App) OpenSession(workspace string) (SessionInfo, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	ws := a.workspace
	passport, tenant := a.configPassport, a.passportTenant
	a.mu.Unlock()
	dir := ws
	if strings.TrimSpace(workspace) != "" {
		abs, err := resolveWorkspaceDir(workspace)
		if err != nil {
			return SessionInfo{}, wireError(err)
		}
		dir = abs
	}
	cfg := a.configForWorkspace(dir)
	if cfg.Model == "" || ws == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	cfg.Resume = ""
	cfg.Continue = false
	cfg.SessionID = ""
	info, err := a.addSessionHeld(cfg, passport, tenant)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	// 换了目录才写配置：记进最近工作区（MRU），下次启动能预填/重开。开在当前
	// 目录时什么都没变，不必为每次「新建对话」都写一遍磁盘。
	if dir != ws {
		req := a.LoadConfig()
		req.CWD = dir
		saveConfig(req)
	}
	return info, nil
}

// FocusSession 把聚焦切到指定会话，并返回它的状态。
//
// 聚焦不只是界面高亮：没带会话 id 的命令都打给它（见 entryOf），而工作区寻址的
// 那批命令（文件列表、技能、子代理、MCP、记忆、会话列表）读的是 a.workspace，
// 所以这里必须一并把工作区搬过去——否则切到另一个目录的会话后，文件浏览器还
// 停在上一条会话的目录上，而且不会有任何报错。
func (a *App) FocusSession(sessionID string) (SessionInfo, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return SessionInfo{}, wireError(errors.New("未指定要聚焦的会话"))
	}
	a.mu.Lock()
	e := a.entryLocked(id)
	if e == nil || e.closed {
		a.mu.Unlock()
		return SessionInfo{}, wireError(errNoSession)
	}
	a.focusLocked(e)
	a.mu.Unlock()
	return a.Status(e.id)
}

// CloseSession 关掉一条会话（空串 = 聚焦的那条）。
//
// 与替换式打开时的关闭不同：那时条目要留着（界面上旧对话还在，「已编辑」卡片
// 得能继续复审）；用户主动关掉一条会话时它从界面上整个消失，条目一并移除，
// 聚焦顺势落到还开着的另一条上。
func (a *App) CloseSession(sessionID string) error {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	var e *sessionEntry
	if strings.TrimSpace(sessionID) == "" {
		e = a.entryLocked(a.focused)
	} else {
		e = a.entryLocked(strings.TrimSpace(sessionID))
	}
	a.mu.Unlock()
	a.closeEntryHeld(e, true)
	return nil
}

// OpenSessions 列出此刻**开着**的会话，供界面画会话列表。
//
// 只报后端独有的事实：是谁、在哪个目录、有没有回合在跑、哪条是聚焦的。标题走
// session:renamed 事件与 ListSessions（工作区里存下来的标题），待审批数在前端的
// 授权队列里（usePermissionQueue 的 waiting）——都不必在这里重复一遍。
//
// **按打开先后排序，不是按 map 的遍历序。** Go 的 map 遍历是随机的：不排的话，
// 界面每回读一次列表，行就重新洗一次牌。用户点一行、列表跳一下、再点同一个位置
// 就点到了别人身上——而这一栏里每行右侧都有一颗关闭按钮，代价是关掉一条正在跑
// 的会话，没有任何报错。
func (a *App) OpenSessions() []OpenSessionInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	live := make([]*sessionEntry, 0, len(a.sessions))
	for _, e := range a.sessions {
		if !e.closed {
			live = append(live, e)
		}
	}
	sort.Slice(live, func(i, j int) bool { return live[i].seq < live[j].seq })
	out := make([]OpenSessionInfo, 0, len(live))
	for _, e := range live {
		out = append(out, OpenSessionInfo{
			SessionID: e.id,
			Workspace: e.workspace,
			Running:   e.turnActive,
			Focused:   e.id == a.focused,
		})
	}
	return out
}

// CloseAllSessions 关掉所有开着的会话。退出时用：多会话之后「关掉当前那条」不再
// 等于「收干净」，漏掉的会话会带着未收尾的回合和未落盘的记录一起被进程带走。
func (a *App) CloseAllSessions() error {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	all := make([]*sessionEntry, 0, len(a.sessions))
	for _, e := range a.sessions {
		all = append(all, e)
	}
	a.mu.Unlock()
	for _, e := range all {
		a.closeEntryHeld(e, true)
	}
	return nil
}
