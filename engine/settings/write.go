package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// WriteUserMCPServers replaces the [mcp.servers] section of the user-level
// config.toml with the given servers, preserving every other setting in the file
// (other mcp keys like allow_sampling, plus provider/hooks/etc.). The file is
// re-encoded, so comments and key ordering are not preserved — a deliberate
// tradeoff so the desktop and CLI share one MCP config source. It is written 0600
// since config.toml may carry credentials.
//
// userConfigDir is the per-user config root (os.UserConfigDir()); the file lives at
// <userConfigDir>/runcode/config.toml. Passing an empty map removes the servers
// section entirely.
func WriteUserMCPServers(userConfigDir string, servers map[string]MCPServerConfig) error {
	if userConfigDir == "" {
		return errors.New("settings: user config dir is required to write MCP config")
	}
	path := filepath.Join(userConfigDir, AppDirName, UserFileName)

	// Decode the whole file into a generic tree so untouched settings survive the
	// round-trip verbatim (values, not formatting).
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("settings: parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("settings: read %s: %w", path, err)
	}

	mcp, _ := root["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	if len(servers) == 0 {
		delete(mcp, "servers")
	} else {
		mcp["servers"] = mcpServersToTree(servers)
	}
	if len(mcp) == 0 {
		delete(root, "mcp")
	} else {
		root["mcp"] = mcp
	}

	out, err := toml.Marshal(root)
	if err != nil {
		return fmt.Errorf("settings: encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("settings: create config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("settings: write %s: %w", path, err)
	}
	return nil
}

// mcpServersToTree renders the servers as a generic tree, emitting only the fields
// that are set so the written file stays minimal.
func mcpServersToTree(servers map[string]MCPServerConfig) map[string]any {
	out := make(map[string]any, len(servers))
	for name, s := range servers {
		m := map[string]any{}
		if s.Transport != "" {
			m["transport"] = s.Transport
		}
		if s.Command != "" {
			m["command"] = s.Command
		}
		if len(s.Args) > 0 {
			m["args"] = s.Args
		}
		if len(s.Env) > 0 {
			m["env"] = s.Env
		}
		if s.Dir != "" {
			m["dir"] = s.Dir
		}
		if s.URL != "" {
			m["url"] = s.URL
		}
		if len(s.Headers) > 0 {
			m["headers"] = s.Headers
		}
		if s.Enabled != nil {
			m["enabled"] = *s.Enabled
		}
		out[name] = m
	}
	return out
}
