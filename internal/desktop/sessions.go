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

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/mcp"
	"github.com/wt68/runcode/engine/sessions"
	"github.com/wt68/runcode/engine/tools"
)

// ToolInfo is a tool's name and description for the @-mention picker.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Source classifies where the tool comes from: "mcp" for a Model Context
	// Protocol server tool, "builtin" for everything else (core tools, Skill, Task).
	Source string `json:"source"`
	// Server is the MCP server name when Source == "mcp".
	Server string `json:"server,omitempty"`
	// ConcurrencySafe reports whether the tool can run in parallel with siblings.
	ConcurrencySafe bool `json:"concurrencySafe"`
	// Toggleable is true for tools the user may turn off (built-in work tools and
	// MCP tools); false for infrastructure tools (Skill/Task/Remember/preview).
	Toggleable bool `json:"toggleable"`
	// DisabledUser / DisabledProject report whether the tool is turned off at that
	// scope. Effective-enabled = neither is true. Disabling takes effect on the
	// next new session.
	DisabledUser    bool `json:"disabledUser"`
	DisabledProject bool `json:"disabledProject"`
}

// ListTools returns the full catalog of built-in tools (always, so a disabled one
// still shows and can be re-enabled) plus the active session's MCP and infra
// tools, each annotated with source, concurrency-safety, and per-scope disabled
// state. Built-in work tools and MCP tools are toggleable; infra tools are not.
func (a *App) ListTools() []ToolInfo {
	a.mu.Lock()
	session := a.session
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

// SessionSummary describes a saved session for the sidebar's recent list.
type SessionSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	When  string `json:"when"`
	Turns int    `json:"turns"`
}

// ResumedBlock is one rendered item of a reopened conversation. Its kinds mirror
// the live chat view's blocks so the frontend can repaint a session as it first
// appeared — user/assistant bubbles plus tool execution cards — rather than a
// flattened text-only transcript.
type ResumedBlock struct {
	Kind string       `json:"kind"` // "user" | "assistant" | "tool"
	Text string       `json:"text,omitempty"`
	Tool *ResumedTool `json:"tool,omitempty"`
}

// ResumedTool is a reconstructed tool step. The persisted history stores only the
// LLM messages, so live-only UI details (colored diffs, file-change chips) are not
// recoverable; the tool name, target path, and result text are.
type ResumedTool struct {
	ToolName  string `json:"toolName"`
	ToolUseID string `json:"toolUseId"`
	Path      string `json:"path,omitempty"`
	Input     string `json:"input,omitempty"` // the tool call's raw arguments JSON
	IsError   bool   `json:"isError"`
	Output    string `json:"output,omitempty"`
}

// ResumedSession carries a reopened session's status plus its prior conversation
// as rendered blocks so the frontend can repaint it.
type ResumedSession struct {
	Info   SessionInfo    `json:"info"`
	Blocks []ResumedBlock `json:"blocks"`
	// ContextTokens is an estimate of the reopened history's context occupancy, so
	// the usage bar shows a sensible value immediately instead of 0 (no turn has run
	// yet to report an exact count). The first turn replaces it with the real value.
	ContextTokens int `json:"contextTokens"`
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
		return nil, err
	}
	defer backend.Close(context.Background())
	infos, err := backend.List(context.Background())
	if err != nil {
		return nil, err
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

// ResumeSession reopens a saved session by id (reusing the active session's
// provider/model/credentials) and returns its prior conversation for display.
func (a *App) ResumeSession(id string) (ResumedSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Model == "" || a.workspace == "" {
		return ResumedSession{}, errNoSession
	}
	cfg := a.config
	cfg.Resume = strings.TrimSpace(id)
	cfg.Continue = false
	cfg.SessionID = ""
	info, err := a.buildAndSetLocked(cfg)
	if err != nil {
		return ResumedSession{}, err
	}
	return ResumedSession{
		Info:          info,
		Blocks:        toResumedBlocks(a.session.History()),
		ContextTokens: a.session.EstimateContextTokens(),
	}, nil
}

// PickWorkspaceFolder opens a native directory picker and returns the chosen path
// ("" when cancelled). The frontend passes the result to SwitchWorkspace.
func (a *App) PickWorkspaceFolder() (string, error) {
	a.mu.Lock()
	dialog := a.dialog
	a.mu.Unlock()
	if dialog == nil {
		return "", errors.New("当前环境不支持目录选择")
	}
	return dialog.PickFolder("选择工作区目录")
}

// SwitchWorkspace closes the current session and opens a fresh one in dir,
// reusing the active session's provider/model/credentials. The chosen directory
// is persisted so the next launch reopens it.
func (a *App) SwitchWorkspace(dir string) (SessionInfo, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return SessionInfo{}, errors.New("未选择目录")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return SessionInfo{}, fmt.Errorf("解析目录: %w", err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return SessionInfo{}, fmt.Errorf("目录不存在: %s", abs)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Model == "" {
		return SessionInfo{}, errNoSession
	}
	cfg := a.config
	cfg.CWD = abs
	cfg.Resume = ""
	cfg.Continue = false
	cfg.SessionID = ""
	a.workspace = abs
	info, err := a.buildAndSetLocked(cfg)
	if err != nil {
		return SessionInfo{}, err
	}
	// Persist the new workspace so the next launch prefills/reopens it.
	req := a.LoadConfig()
	req.CWD = abs
	saveConfig(req)
	return info, nil
}

// NewSession opens a fresh session in the same workspace (a new id, empty
// history), reusing the active session's provider/model/credentials.
func (a *App) NewSession() (SessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config.Model == "" || a.workspace == "" {
		return SessionInfo{}, errNoSession
	}
	cfg := a.config
	cfg.Resume = ""
	cfg.Continue = false
	cfg.SessionID = ""
	return a.buildAndSetLocked(cfg)
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
