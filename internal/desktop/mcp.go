package desktop

import (
	"errors"
	"os"
	"sort"
	"strings"
	"sync"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
)

// mcpMu serializes the load→mutate→write cycles on the user-level MCP server
// config. Save/Delete/SetEnabled are Wails-bound and can overlap; without the
// lock, two cycles reading the same snapshot would silently lose the earlier
// write. (Atomicity of the file write itself is the engine's concern —
// settings.WriteUserMCPServers owns the file format and the write.)
var mcpMu sync.Mutex

// ListMCPServers returns every configured MCP server (from the shared
// config.toml), annotated with live connection state from the running session.
func (a *App) ListMCPServers() ([]MCPServerInfo, error) {
	servers, err := a.loadMCPServers()
	if err != nil {
		return nil, wireError(err)
	}

	live := map[string]mcp.ServerStatus{}
	toolsByServer := map[string][]MCPToolBrief{}
	if session, err := a.engineSession(); err == nil {
		for _, s := range session.MCPStatus() {
			live[s.Name] = s
		}
		// Group the session's live tools by server (mcp__server__tool) so each
		// server can list its own capabilities on the MCP page.
		for _, d := range session.ToolList() {
			if server, short, ok := mcp.ParseToolName(d.Name); ok {
				toolsByServer[server] = append(toolsByServer[server], MCPToolBrief{Name: short, Description: d.Description})
			}
		}
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]MCPServerInfo, 0, len(names))
	for _, name := range names {
		raw := servers[name]
		st, connected := live[name]
		out = append(out, MCPServerInfo{
			Name:      name,
			Transport: transportOrDefault(raw.Transport),
			Command:   raw.Command,
			Args:      raw.Args,
			Env:       raw.Env,
			Dir:       raw.Dir,
			URL:       raw.URL,
			Headers:   raw.Headers,
			Passport:  mcpPassportEnabled(raw),
			Enabled:   raw.Enabled == nil || *raw.Enabled,
			Connected: connected,
			ToolCount: st.ToolCount,
			Tools:     toolsByServer[name],
		})
	}
	return out, nil
}

// SaveMCPServer creates or updates a server and persists it to config.toml. It
// validates the server (name and transport-specific required fields) before
// writing, so a bad entry is rejected with a clear error instead of silently
// breaking the next session's startup.
func (a *App) SaveMCPServer(in MCPServerInput) error {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return wireError(errors.New("服务器名称不能为空"))
	}

	cfg := settings.MCPServerConfig{
		Transport: strings.TrimSpace(in.Transport),
		Command:   strings.TrimSpace(in.Command),
		Args:      trimmedNonEmpty(in.Args),
		Env:       nonEmptyMap(in.Env),
		Dir:       strings.TrimSpace(in.Dir),
		URL:       strings.TrimSpace(in.URL),
		Headers:   nonEmptyMap(in.Headers),
		Enabled:   boolPointer(in.Enabled),
	}
	cfg = withMCPPassport(cfg, in.Passport)

	// Validate the entry as if enabled, so required fields are checked even when the
	// user saves it disabled.
	probe := cfg
	probe.Enabled = nil
	if _, err := engine.MCPServersFromConfig(settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{name: probe}}); err != nil {
		return wireError(err)
	}

	mcpMu.Lock()
	defer mcpMu.Unlock()
	servers, err := a.loadMCPServers()
	if err != nil {
		return wireError(err)
	}
	if orig := strings.TrimSpace(in.OriginalName); orig != "" && orig != name {
		delete(servers, orig)
	}
	servers[name] = cfg
	return wireError(a.writeMCPServers(servers))
}

// DeleteMCPServer removes a server from config.toml.
func (a *App) DeleteMCPServer(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return wireError(errors.New("服务器名称不能为空"))
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	servers, err := a.loadMCPServers()
	if err != nil {
		return wireError(err)
	}
	delete(servers, name)
	return wireError(a.writeMCPServers(servers))
}

// SetMCPServerEnabled toggles a server on or off without touching its other fields.
func (a *App) SetMCPServerEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	mcpMu.Lock()
	defer mcpMu.Unlock()
	servers, err := a.loadMCPServers()
	if err != nil {
		return wireError(err)
	}
	server, ok := servers[name]
	if !ok {
		return wireError(errors.New("未找到该 MCP 服务器"))
	}
	server.Enabled = boolPointer(enabled)
	servers[name] = server
	return wireError(a.writeMCPServers(servers))
}

// ReloadMCPServers applies MCP config changes to the running session right away,
// by rebuilding it on its own id so the conversation is restored from the store
// and every server reconnects from the current config.toml. It reports whether a
// session was actually rebuilt: with no live session there is nothing to do — the
// next one reads the new config anyway.
//
// Rebuilding mid-turn would drop the in-flight tool calls, so a busy session is
// refused and the caller is told to retry; the change still lands on the next
// session either way.
func (a *App) ReloadMCPServers() (bool, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	id, cfg, busy := a.currentID, a.config, a.turnActive
	a.mu.Unlock()
	if id == "" || cfg.Model == "" {
		return false, nil
	}
	if busy {
		return false, wireError(errors.New("回合进行中，本次改动将在回合结束后新建会话时生效"))
	}
	// Resume the same id: the engine reloads this conversation's history, so the
	// user keeps their chat while the tool set changes underneath.
	cfg.Resume = id
	cfg.Continue = false
	cfg.SessionID = ""
	if _, err := a.openSessionHeld(cfg); err != nil {
		return false, wireError(err)
	}
	return true, nil
}

func (a *App) loadMCPServers() (map[string]settings.MCPServerConfig, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	res, err := settings.Load(settings.LoadOptions{UserConfigDir: dir})
	if err != nil {
		return nil, err
	}
	servers := res.Config.MCP.Servers
	if servers == nil {
		return map[string]settings.MCPServerConfig{}, nil
	}
	return servers, nil
}

func (a *App) writeMCPServers(servers map[string]settings.MCPServerConfig) error {
	dir, err := os.UserConfigDir()
	if err != nil {
		return wireError(err)
	}
	return settings.WriteUserMCPServers(dir, servers)
}

func transportOrDefault(t string) string {
	if strings.TrimSpace(t) == "" {
		return "stdio"
	}
	return strings.ToLower(strings.TrimSpace(t))
}

func boolPointer(b bool) *bool { return &b }

func trimmedNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if s := strings.TrimSpace(v); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func nonEmptyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k = strings.TrimSpace(k); k != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
