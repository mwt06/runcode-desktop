package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools"
)

// ListTools returns the full catalog of built-in tools (always, so a disabled one
// still shows and can be re-enabled) plus the active session's MCP and infra
// tools, each annotated with source, concurrency-safety, and per-scope disabled
// state. Built-in work tools and MCP tools are toggleable; infra tools are not.
func (a *App) ListTools() []ToolInfo {
	session, _ := a.engineSession() // nil without a live session; static catalog still lists
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()

	uc, pc := scopeDisabled(ws)
	userTools, projTools := toStringSet(uc.Tools), toStringSet(pc.Tools)
	out := make([]ToolInfo, 0, 24)
	seen := make(map[string]bool)
	add := func(name, desc string, safe bool, source, server string, toggleable bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, ToolInfo{
			Name: name, Description: desc, ConcurrencySafe: safe,
			Source: source, Server: server, Toggleable: toggleable,
			DisabledUser: userTools[name], DisabledProject: projTools[name],
		})
	}

	// Built-in work tools: enumerated statically so a disabled one still appears.
	for _, t := range tools.Builtins() {
		add(t.Name(), t.Description(), t.IsConcurrencySafe(), "builtin", "", true)
	}
	// Session tools: MCP (toggleable) and infra (Skill/Task/Remember/preview, not).
	if session != nil {
		for _, d := range session.ToolList() {
			if server, _, ok := mcp.ParseToolName(d.Name); ok {
				add(d.Name, d.Description, d.ConcurrencySafe, "mcp", server, true)
			} else {
				add(d.Name, d.Description, d.ConcurrencySafe, "builtin", "", false)
			}
		}
	}
	// Disabled MCP tools not in the current session still show so they can be
	// re-enabled (a fresh session started with them off would not list them).
	for name := range unionSets(userTools, projTools) {
		if server, _, ok := mcp.ParseToolName(name); ok {
			add(name, "(已停用的 MCP 工具)", false, "mcp", server, true)
		}
	}
	return out
}

func unionSets(a, b map[string]bool) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}

// ListSessions returns the workspace's saved sessions, newest first, for the
// recent-conversations list. It returns nil when no session has been started yet.
func (a *App) ListSessions() ([]SessionSummary, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" {
		return nil, nil
	}
	backend, err := sessions.OpenBackend(ws, sessions.BackendJSONL)
	if err != nil {
		return nil, wireError(err)
	}
	defer func() { _ = backend.Close(context.Background()) }()
	infos, err := backend.List(context.Background())
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]SessionSummary, 0, len(infos))
	for _, info := range infos {
		// Prefer the model-generated session name; fall back to the raw prompts.
		title := firstNonEmpty(info.Title, info.LastUser, info.FirstUser, info.ID)
		out = append(out, SessionSummary{
			ID:    info.ID,
			Title: title,
			When:  humanizeSince(info.ModTime),
			Turns: info.Turns,
		})
	}
	return out, nil
}

// DeleteSession permanently removes a saved session (history + title/meta
// sidecars) from the workspace store.
//
// 活动会话不可删除。它此前被允许("只是让它不可恢复"),但那个说法已不成立:
// 引擎的 JSONLStore 按批 open/append/close 且带 O_CREATE,所以删掉文件后的下一个
// 回合会把它重新创建出来——留下一个只含删除后内容、历史却已丢失的僵尸会话,反而
// 比不删更糟。要删就先新建或切到别的会话。
func (a *App) DeleteSession(id string) error {
	a.mu.Lock()
	ws := a.workspace
	current := ""
	if e := a.liveEntryLocked(); e != nil {
		current = e.id
	}
	a.mu.Unlock()
	id = strings.TrimSpace(id)
	if ws == "" || id == "" {
		return wireError(errors.New("无效的会话"))
	}
	if current != "" && id == current {
		return wireError(errors.New("不能删除当前正在进行的会话；请先新建或切换到其它会话再删除"))
	}
	backend, err := sessions.OpenBackend(ws, sessions.BackendJSONL)
	if err != nil {
		return wireError(err)
	}
	defer func() { _ = backend.Close(context.Background()) }()
	deleter, ok := backend.(sessions.Deleter)
	if !ok {
		return wireError(errors.New("当前会话存储不支持删除"))
	}
	if err := deleter.Delete(context.Background(), id); err != nil {
		return wireError(err)
	}
	return nil
}

// ResumeSession reopens a saved session by id (reusing the active session's
// provider/model/credentials) and returns its prior conversation for display.
//
// **它是加开一条，不关掉任何已经开着的会话。** 曾经是替换式打开（先关掉聚焦的那条
// 再按 id 重建），于是"点一下「最近对话」"会把正在跑的活当场停掉——这是同一个坑的
// 第三次报障（前两次是「新建对话」与「换工作区」）。要恢复的那条本来就开着时只
// 切聚焦（focusIfAlreadyOpenHeld），重复点不会重建。
//
// 代价是会话会攒起来：每点开一条历史对话就多一条活着的会话（各自占着 MCP 连接与
// 会话存储句柄）。这是可见且可撤销的——它们都列在「打开中」里，关掉是一个明确的
// 动作；而"悄悄弄丢正在跑的活"既不可见也不可撤销。上限留给 host.Limits.MaxSessions。
func (a *App) ResumeSession(id string) (ResumedSession, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	// 「最近对话」列的是**聚焦这个工作区**里存着的会话（ListSessions 读 a.workspace），
	// 所以恢复也必须在这个目录里进行——不能照抄 a.config 里那份可能已经过期的 CWD。
	cfg := a.configForWorkspace(ws)
	if cfg.Model == "" || ws == "" {
		return ResumedSession{}, wireError(errNoSession)
	}
	cfg.Resume = strings.TrimSpace(id)
	cfg.Continue = false
	cfg.SessionID = ""
	a.mu.Lock()
	passport, tenant := a.configPassport, a.passportTenant
	a.mu.Unlock()
	info, err := a.addSessionHeld(cfg, passport, tenant)
	if err != nil {
		return ResumedSession{}, wireError(err)
	}
	session, err := a.currentSession()
	if err != nil {
		return ResumedSession{}, wireError(err)
	}
	return ResumedSession{
		Info:          info,
		Blocks:        toResumedBlocks(session.History()),
		ContextTokens: session.EstimateContextTokens(),
	}, nil
}

// PickWorkspaceFolder opens a native directory picker and returns the chosen path
// ("" when cancelled). The frontend passes the result to OpenSession.
func (a *App) PickWorkspaceFolder() (string, error) {
	a.mu.Lock()
	dialog := a.dialog
	a.mu.Unlock()
	if dialog == nil {
		return "", wireError(errors.New("当前环境不支持目录选择"))
	}
	dir, err := dialog.PickFolder("选择工作区目录", "")
	return dir, wireError(err)
}

// resolveWorkspaceDir 把用户选的目录归一成绝对路径并确认它真的是个目录。
//
// 换工作区曾经是 SwitchWorkspace（关掉当前会话、在新目录重开一条）。多工作区并行
// 之后它整个让位给 OpenSession(workspace)——换目录不再要求先丢掉手上的会话，
// 而"关掉一条会话"有它自己的明确入口（CloseSession）。这个函数是那次搬家里唯一
// 值得留下的部分。
func resolveWorkspaceDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", errors.New("未选择目录")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("解析目录: %w", err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("目录不存在: %s", abs)
	}
	return abs, nil
}

// NewSession opens a fresh session in the same workspace (a new id, empty
// history), reusing the active session's provider/model/credentials.
//
// **它是替换式打开：先关掉当前那条会话，正在跑的回合会被当场取消。** 多会话之后
// 界面上的「新建对话」走的是 OpenSession（加开一条，不动已开的）；这条留给"我要
// 换一条干净的对话、旧的不要了"那个语义。
//
// 两个命令长得像而后果差很远，真踩过：界面上曾按 openList 是否为空来二选一，而
// 首个会话还没进那个列表，于是点「新建对话」走成了这一条，用户刚发出去的回合
// 当场停掉。判据后来换成了"有没有会话"（session.info），不再依赖异步回读的列表。
func (a *App) NewSession() (SessionInfo, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	cfg := a.configForWorkspace(ws) // 在**聚焦这条会话的目录**里开，不是 a.config 记的那个
	if cfg.Model == "" || ws == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	cfg.Resume = ""
	cfg.Continue = false
	cfg.SessionID = ""
	info, err := a.openSessionHeld(cfg)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	return info, nil
}

// toResumedBlocks reconstructs a conversation's rendered blocks from its message
// history: user/assistant text bubbles plus a tool block per tool result. Tool
// blocks are paired with their originating tool_use (by id) so the tool name and
// target path survive. Empty (tool-only) assistant turns, thinking blocks, and
// injected hook/session-start context are dropped, matching the live view.
func toResumedBlocks(history []llm.Message) []ResumedBlock {
	// First pass: index every tool_use by id so a later tool_result can recover its
	// tool name and target path.
	type toolMeta struct{ name, path, input string }
	meta := map[string]toolMeta{}
	for _, m := range history {
		if m.Role != llm.RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			if b.Type == llm.ContentBlockTypeToolUse {
				meta[b.ID] = toolMeta{name: b.Name, path: toolInputPath(b.Input), input: string(b.Input)}
			}
		}
	}

	out := make([]ResumedBlock, 0, len(history))
	for _, m := range history {
		switch m.Role {
		case llm.RoleUser:
			text := strings.TrimSpace(llm.TextContent(m))
			if text == "" || strings.HasPrefix(text, "Additional context from") {
				continue
			}
			out = append(out, ResumedBlock{Kind: "user", Text: text})
		case llm.RoleAssistant:
			if text := strings.TrimSpace(llm.TextContent(m)); text != "" {
				out = append(out, ResumedBlock{Kind: "assistant", Text: text})
			}
		case llm.RoleTool:
			for _, b := range m.Content {
				if b.Type != llm.ContentBlockTypeToolResult {
					continue
				}
				mt := meta[b.ToolUseID]
				out = append(out, ResumedBlock{Kind: "tool", Tool: &ResumedTool{
					ToolName:  mt.name,
					ToolUseID: b.ToolUseID,
					Path:      mt.path,
					Input:     mt.input,
					IsError:   b.IsError,
					Output:    toolResultText(b),
				}})
			}
		}
	}
	return out
}

// toolInputPath extracts a "path" field from a tool_use input, used to label the
// resumed tool step (e.g. the file a Write targeted). "" when absent.
func toolInputPath(input json.RawMessage) string {
	var in struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &in)
	return in.Path
}

// toolResultText flattens a tool_result block's text into one string.
func toolResultText(block llm.ContentBlock) string {
	parts := make([]string, 0, len(block.Content)+1)
	if block.Text != "" {
		parts = append(parts, block.Text)
	}
	for _, inner := range block.Content {
		if inner.Type == llm.ContentBlockTypeText && inner.Text != "" {
			parts = append(parts, inner.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// humanizeSince renders a timestamp as a short, friendly Chinese relative label.
func humanizeSince(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 48*time.Hour:
		return "昨天"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	default:
		return t.Format("01-02")
	}
}
