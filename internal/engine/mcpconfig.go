package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/internal/persistence/settings"
)

// MCPServersFromConfig converts the user-level MCP config into connectable server
// configs. It expands ${VAR} references (so secrets stay in the environment),
// applies the enabled-by-default rule, and validates each server. Servers are
// returned in name order for deterministic startup. A misconfigured server is a
// hard error so the user fixes it rather than silently losing a tool source.
//
// It lives in the engine so the CLI and the desktop interpret config.toml
// identically instead of each carrying its own copy of the rules.
func MCPServersFromConfig(cfg settings.MCPConfig) ([]mcp.ServerConfig, error) {
	if len(cfg.Servers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var servers []mcp.ServerConfig
	for _, name := range names {
		if !validMCPServerName(name) {
			return nil, fmt.Errorf("mcp server name %q is invalid (use letters, digits, '-' or '_', and no '__')", name)
		}
		raw := cfg.Servers[name]
		if raw.Enabled != nil && !*raw.Enabled {
			continue
		}
		server, err := mcpServerConfig(name, raw)
		if err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}
	return servers, nil
}

func mcpServerConfig(name string, raw settings.MCPServerConfig) (mcp.ServerConfig, error) {
	transport := mcp.TransportType(strings.ToLower(strings.TrimSpace(raw.Transport)))
	if transport == "" {
		transport = mcp.TransportStdio
	}
	server := mcp.ServerConfig{Name: name, Transport: transport}

	switch transport {
	case mcp.TransportStdio:
		server.Command = expandEnv(raw.Command)
		if server.Command == "" {
			return mcp.ServerConfig{}, fmt.Errorf("mcp server %q: command is required for stdio transport", name)
		}
		server.Args = expandEnvAll(raw.Args)
		server.Env = expandEnvPairs(raw.Env)
		server.Dir = expandEnv(raw.Dir)
	case mcp.TransportHTTP:
		server.URL = expandEnv(raw.URL)
		if server.URL == "" {
			return mcp.ServerConfig{}, fmt.Errorf("mcp server %q: url is required for http transport", name)
		}
		server.Headers = expandEnvMap(raw.Headers)
	default:
		return mcp.ServerConfig{}, fmt.Errorf("mcp server %q: unknown transport %q (want stdio or http)", name, raw.Transport)
	}
	return server, nil
}

// validMCPServerName allows the namespacing convention to round-trip: no "__"
// (which separates server from tool in mcp__server__tool) and only name-safe
// characters.
func validMCPServerName(name string) bool {
	if name == "" || strings.Contains(name, "__") {
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

func expandEnv(value string) string {
	if value == "" {
		return ""
	}
	return os.Expand(value, os.Getenv)
}

func expandEnvAll(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = expandEnv(v)
	}
	return out
}

// expandEnvPairs renders an env map as "KEY=VALUE" entries with expanded values.
func expandEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, k := range keys {
		out = append(out, k+"="+expandEnv(env[k]))
	}
	return out
}

func expandEnvMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = expandEnv(v)
	}
	return out
}
