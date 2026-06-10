// Package settings loads runcode configuration from TOML files, layering a
// user-level file and a project-level file beneath command-line flags and
// environment variables. Credentials are only honored from the user-level file.
package settings

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/wt68/runcode/internal/toolpath"
)

// utf8BOM is the byte-order mark some Windows editors prepend; TOML files should
// not have one, but tolerating it avoids confusing parse errors.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

const (
	// ProjectFileName is the project-level config file discovered by walking up
	// from the working directory.
	ProjectFileName = "runcode.toml"
	// UserFileName is the config file inside the per-user app directory.
	UserFileName = "config.toml"
	// AppDirName is the runcode subdirectory within the user config directory.
	AppDirName = "runcode"

	maxConfigBytes = 64 * 1024
)

// Config is the on-disk TOML schema. Empty string and nil pointer fields mean
// "not set" and let lower-priority sources (env, defaults) take over.
type Config struct {
	Provider           string   `toml:"provider"`
	Model              string   `toml:"model"`
	BaseURL            string   `toml:"base_url"`
	MaxTokens          *int     `toml:"max_tokens"`
	PermissionMode     string   `toml:"permission_mode"`
	Telemetry          string   `toml:"telemetry"`
	Transcript         string   `toml:"transcript"`
	SessionBackend     string   `toml:"session_backend"`
	MaxHistoryMessages *int     `toml:"max_history_messages"`
	MaxContextTokens   *int     `toml:"max_context_tokens"`
	MaxRetries         *int     `toml:"max_retries"`
	InputPrice         *float64 `toml:"input_price"`
	OutputPrice        *float64 `toml:"output_price"`
	APIKey             string   `toml:"api_key"`
	AuthToken          string   `toml:"auth_token"`
	// MCP configures Model Context Protocol servers. It is honored only from the
	// user-level file (like credentials): a project file must not be able to make
	// runcode launch subprocesses or connect to endpoints just by being present.
	MCP MCPConfig `toml:"mcp"`
	// Hooks configures lifecycle hook commands. Like MCP, it is honored only from
	// the user-level file: a project file must not be able to run arbitrary
	// commands just by being opened.
	Hooks []HookConfig `toml:"hooks"`
}

// HookConfig describes one lifecycle hook. The command is run directly (no
// shell) with the event payload on stdin.
type HookConfig struct {
	Event     string   `toml:"event"`      // PreToolUse | PostToolUse | UserPromptSubmit
	Matcher   string   `toml:"matcher"`    // tool name or "*" (tool events only)
	Command   []string `toml:"command"`    // executable + args
	TimeoutMS int      `toml:"timeout_ms"` // 0 = default
}

// MCPConfig groups the configured MCP servers keyed by a short name.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `toml:"servers"`
	// AllowSampling opts in to serving server sampling/createMessage requests
	// (a server using runcode's model). Default false; honored only from the
	// user-level file, like the rest of the MCP config.
	AllowSampling *bool `toml:"allow_sampling"`
}

// MCPServerConfig describes one MCP server. String values support ${VAR}
// expansion so secrets stay in environment variables rather than the file.
type MCPServerConfig struct {
	Transport string            `toml:"transport"` // "stdio" (default) or "http"
	Command   string            `toml:"command"`   // stdio: executable
	Args      []string          `toml:"args"`      // stdio: arguments
	Env       map[string]string `toml:"env"`       // stdio: extra environment
	Dir       string            `toml:"dir"`       // stdio: working directory
	URL       string            `toml:"url"`       // http: endpoint
	Headers   map[string]string `toml:"headers"`   // http: extra headers
	Enabled   *bool             `toml:"enabled"`   // default true
}

// LoadOptions controls config discovery.
type LoadOptions struct {
	// CWD is the working directory the project-level search starts from.
	CWD string
	// UserConfigDir is the per-user config root (e.g. os.UserConfigDir()); the
	// user file is read from <UserConfigDir>/runcode/config.toml. Empty disables
	// the user layer (useful in tests).
	UserConfigDir string
}

// Resolved is the merged configuration plus the file paths that were loaded.
type Resolved struct {
	Config      Config
	ProjectPath string
	UserPath    string
}

// Load reads the user-level and project-level config files and merges them, with
// project values overriding user values. Credentials are taken only from the
// user-level file; any credentials in the project file are discarded. A missing
// file is not an error.
func Load(opts LoadOptions) (Resolved, error) {
	resolved := Resolved{}

	if opts.UserConfigDir != "" {
		path := filepath.Join(opts.UserConfigDir, AppDirName, UserFileName)
		cfg, ok, err := readConfigFile("", path)
		if err != nil {
			return Resolved{}, err
		}
		if ok {
			resolved.Config = cfg
			resolved.UserPath = path
		}
	}

	projectCfg, projectPath, err := discoverProject(opts.CWD)
	if err != nil {
		return Resolved{}, err
	}
	if projectPath != "" {
		// Credentials never come from a project file (it may be committed to VCS).
		projectCfg.APIKey = ""
		projectCfg.AuthToken = ""
		// MCP servers never come from a project file either: honoring them would
		// let a cloned repo launch arbitrary subprocesses or reach endpoints just
		// by being opened. They are user-level only.
		projectCfg.MCP = MCPConfig{}
		// Hooks run arbitrary commands, so they are user-level only too — a cloned
		// repo must not be able to run commands just by being opened.
		projectCfg.Hooks = nil
		resolved.Config = merge(resolved.Config, projectCfg)
		resolved.ProjectPath = projectPath
	}

	return resolved, nil
}

func discoverProject(cwd string) (Config, string, error) {
	workspace, err := resolveDir(cwd)
	if err != nil {
		return Config{}, "", err
	}
	for dir := workspace; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, ProjectFileName)
		cfg, ok, err := readConfigFile(dir, candidate)
		if err != nil {
			return Config{}, "", err
		}
		if ok {
			return cfg, candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Config{}, "", nil
		}
	}
}

// readConfigFile reads and parses a TOML config file. When searchDir is
// non-empty, the file must resolve within it (symlink-escape protection). A
// missing or non-regular file yields ok=false with no error.
func readConfigFile(searchDir string, path string) (Config, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, false, nil
		}
		return Config{}, false, fmt.Errorf("stat config %s: %w", path, err)
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return Config{}, false, nil
	}
	if searchDir != "" {
		within, err := toolpath.IsWithinResolved(searchDir, path)
		if err != nil {
			return Config{}, false, fmt.Errorf("check config scope %s: %w", path, err)
		}
		if !within {
			return Config{}, false, nil
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, false, fmt.Errorf("open config %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxConfigBytes)+1))
	if err != nil {
		return Config{}, false, fmt.Errorf("read config %s: %w", path, err)
	}
	if len(data) > maxConfigBytes {
		return Config{}, false, fmt.Errorf("config %s exceeds %d bytes", path, maxConfigBytes)
	}
	data = bytes.TrimPrefix(data, utf8BOM)

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, true, nil
}

// merge returns base with any set fields of override applied on top.
func merge(base Config, override Config) Config {
	out := base
	if override.Provider != "" {
		out.Provider = override.Provider
	}
	if override.Model != "" {
		out.Model = override.Model
	}
	if override.BaseURL != "" {
		out.BaseURL = override.BaseURL
	}
	if override.MaxTokens != nil {
		out.MaxTokens = override.MaxTokens
	}
	if override.PermissionMode != "" {
		out.PermissionMode = override.PermissionMode
	}
	if override.Telemetry != "" {
		out.Telemetry = override.Telemetry
	}
	if override.Transcript != "" {
		out.Transcript = override.Transcript
	}
	if override.SessionBackend != "" {
		out.SessionBackend = override.SessionBackend
	}
	if override.MaxHistoryMessages != nil {
		out.MaxHistoryMessages = override.MaxHistoryMessages
	}
	if override.MaxContextTokens != nil {
		out.MaxContextTokens = override.MaxContextTokens
	}
	if override.MaxRetries != nil {
		out.MaxRetries = override.MaxRetries
	}
	if override.InputPrice != nil {
		out.InputPrice = override.InputPrice
	}
	if override.OutputPrice != nil {
		out.OutputPrice = override.OutputPrice
	}
	if override.APIKey != "" {
		out.APIKey = override.APIKey
	}
	if override.AuthToken != "" {
		out.AuthToken = override.AuthToken
	}
	if len(override.MCP.Servers) > 0 {
		merged := make(map[string]MCPServerConfig, len(out.MCP.Servers)+len(override.MCP.Servers))
		for name, server := range out.MCP.Servers {
			merged[name] = server
		}
		for name, server := range override.MCP.Servers {
			merged[name] = server // override wins per server name
		}
		out.MCP.Servers = merged
	}
	return out
}

func resolveDir(cwd string) (string, error) {
	if cwd == "" {
		current, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
		cwd = current
	}
	abs, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return abs, nil
}
