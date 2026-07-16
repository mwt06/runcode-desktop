package desktop

import (
	"errors"
	"os"
	"sort"
	"strings"

	"github.com/wt68/runcode/engine"
	"github.com/wt68/runcode/engine/mcp"
	"github.com/wt68/runcode/engine/settings"
)

// ListMCPServers returns every configured MCP server (from the shared
// config.toml), annotated with live connection state from the running session.
func (a *App) ListMCPServers() ([]MCPServerInfo, error) {
	servers, err := a.loadMCPServers()
	if err != nil {
		return nil, err
	}

	live := map[string]mcp.ServerStatus{}
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session != nil {
		for _, s := range session.MCPStatus() {
			live[s.Name] = s
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
			Enabled:   raw.Enabled == nil || *raw.Enabled,
			Connected: connected,
			ToolCount: st.ToolCount,
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
		return errors.New("服务器名称不能为空")
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

	// Validate the entry as if enabled, so required fields are checked even when the
	// user saves it disabled.
	probe := cfg
	probe.Enabled = nil
	if _, err := engine.MCPServersFromConfig(settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{name: probe}}); err != nil {
		return err
	}

	servers, err := a.loadMCPServers()
	if err != nil {
		return err
	}
	if orig := strings.TrimSpace(in.OriginalName); orig != "" && orig != name {
		delete(servers, orig)
	}
	servers[name] = cfg
	return a.writeMCPServers(servers)
}

// DeleteMCPServer removes a server from config.toml.
func (a *App) DeleteMCPServer(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("服务器名称不能为空")
	}
	servers, err := a.loadMCPServers()
	if err != nil {
		return err
	}
	delete(servers, name)
	return a.writeMCPServers(servers)
}

// SetMCPServerEnabled toggles a server on or off without touching its other fields.
func (a *App) SetMCPServerEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	servers, err := a.loadMCPServers()
	if err != nil {
		return err
	}
	server, ok := servers[name]
	if !ok {
		return errors.New("未找到该 MCP 服务器")
	}
	server.Enabled = boolPointer(enabled)
	servers[name] = server
	return a.writeMCPServers(servers)
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
		return err
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
