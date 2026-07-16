package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/wt68/runcode/engine/tool"
)

// TransportType selects how a server is reached.
type TransportType string

const (
	// TransportStdio launches a local subprocess and speaks over its stdio.
	TransportStdio TransportType = "stdio"
	// TransportHTTP connects to a remote server over Streamable HTTP.
	TransportHTTP TransportType = "http"
)

// defaultDialTimeout bounds a single server's handshake and tool listing so one
// slow or hung server cannot block startup indefinitely.
const defaultDialTimeout = 30 * time.Second

// ServerConfig describes one MCP server to connect to.
type ServerConfig struct {
	Name      string
	Transport TransportType

	// stdio
	Command string
	Args    []string
	Env     []string
	Dir     string

	// http
	URL     string
	Headers map[string]string
}

// StartupError records a server that failed to connect. Startup is tolerant: a
// failed server is skipped with one of these recorded, never aborting the others.
type StartupError struct {
	Server string
	Err    error
}

func (e StartupError) Error() string {
	return fmt.Sprintf("mcp server %q: %v", e.Server, e.Err)
}

// Manager owns the connected MCP servers and the tools they contribute.
type Manager struct {
	conns []*serverConn
	tools []tool.Tool
}

// Options configures a Manager beyond the per-server list.
type Options struct {
	// Roots are the filesystem boundaries runcode exposes to every server via
	// roots/list, so a server can learn the workspace it operates in.
	Roots []Root
	// Sampler, when set, lets servers request a model completion via
	// sampling/createMessage. Leaving it nil (the default) means the sampling
	// capability is not advertised and such requests are refused.
	Sampler Sampler
}

// dialFunc connects one server. It is a seam so tests can inject fake servers.
type dialFunc func(ctx context.Context, cfg ServerConfig) (*serverConn, error)

// Open connects every configured server, tolerating failures. It returns a
// Manager exposing the aggregated tools plus a list of per-server startup errors
// (the caller decides how to surface them). The Manager must be Closed to stop
// the subprocesses/connections.
func Open(ctx context.Context, configs []ServerConfig, opts Options) (*Manager, []StartupError) {
	dial := func(ctx context.Context, cfg ServerConfig) (*serverConn, error) {
		return dialServer(ctx, cfg, opts.Roots, opts.Sampler)
	}
	return openWith(ctx, configs, dial)
}

func openWith(ctx context.Context, configs []ServerConfig, dial dialFunc) (*Manager, []StartupError) {
	m := &Manager{}
	seen := make(map[string]struct{})
	var errs []StartupError
	for _, cfg := range configs {
		conn, err := dial(ctx, cfg)
		if err != nil {
			errs = append(errs, StartupError{Server: cfg.Name, Err: err})
			continue
		}
		m.conns = append(m.conns, conn)
		for _, t := range conn.toolList() {
			if _, dup := seen[t.Name()]; dup {
				continue // never expose a duplicate tool name to the executor
			}
			seen[t.Name()] = struct{}{}
			m.tools = append(m.tools, t)
		}
	}
	// Expose the built-in resource and prompt tools once, when at least one
	// connected server supports the corresponding primitive.
	if rs := newResourceServers(m.conns); !rs.empty() {
		m.tools = append(m.tools, rs.tools()...)
	}
	if ps := newPromptServers(m.conns); !ps.empty() {
		m.tools = append(m.tools, ps.tools()...)
	}
	return m, errs
}

// Tools returns the aggregated MCP tools across all connected servers.
func (m *Manager) Tools() []tool.Tool {
	if m == nil {
		return nil
	}
	return m.tools
}

// ServerStatus is a connected server's live state, for surfacing MCP health in a UI.
type ServerStatus struct {
	Name      string
	ToolCount int
}

// Status returns each connected server and how many tools it contributes. Servers
// that failed to connect are absent (their failure is reported as a StartupError by
// Open), so a caller can treat a configured-but-enabled-but-absent server as "not
// connected".
func (m *Manager) Status() []ServerStatus {
	if m == nil {
		return nil
	}
	out := make([]ServerStatus, 0, len(m.conns))
	for _, c := range m.conns {
		out = append(out, ServerStatus{Name: c.name, ToolCount: len(c.tools)})
	}
	return out
}

// Close stops every connected server. It returns the first close error, if any,
// after attempting to close all of them.
func (m *Manager) Close(context.Context) error {
	if m == nil {
		return nil
	}
	var firstErr error
	for _, c := range m.conns {
		if err := c.close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// dialServer is the production dialer: it builds the transport, performs the
// handshake, lists tools, and adapts them. roots are advertised to the server,
// and the sampler (when set) lets the server request model completions. The
// returned serverConn carries a redial closure so a dropped connection is
// re-established on the next call rather than killing the server's tools.
func dialServer(ctx context.Context, cfg ServerConfig, roots []Root, sampler Sampler) (*serverConn, error) {
	sc := &serverConn{name: cfg.Name}
	// onToolsChanged keeps the serverConn's tool slice current when the server
	// announces tools/list_changed (and across reconnects, which reuse this).
	onToolsChanged := func(descriptors []ToolDescriptor) {
		sc.setTools(buildTools(cfg.Name, sc, descriptors))
	}
	sc.redial = func(ctx context.Context) (*Client, error) {
		client, _, err := dialClient(ctx, cfg, roots, sampler, onToolsChanged)
		return client, err
	}
	client, descriptors, err := dialClient(ctx, cfg, roots, sampler, onToolsChanged)
	if err != nil {
		return nil, err
	}
	sc.client = client
	sc.supportsResources = client.SupportsResources()
	sc.supportsPrompts = client.SupportsPrompts()
	// Tools hold the serverConn (not the client) so a later reconnect is
	// transparent. The descriptors fix the exposed tool set at first dial; a
	// reconnect reuses it even if the server's list later changes.
	sc.tools = buildTools(cfg.Name, sc, descriptors)
	return sc, nil
}

// dialClient performs one transport dial + handshake + tool listing, returning a
// ready client and its advertised tools. It is the unit dialServer re-runs to
// reconnect.
func dialClient(ctx context.Context, cfg ServerConfig, roots []Root, sampler Sampler, onToolsChanged func([]ToolDescriptor)) (*Client, []ToolDescriptor, error) {
	stream, err := newTransport(cfg)
	if err != nil {
		return nil, nil, err
	}
	client := newClientWith(stream, clientConfig{roots: roots, sampler: sampler, serverName: cfg.Name, onToolsChanged: onToolsChanged})
	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	if err := client.Initialize(dialCtx); err != nil {
		_ = client.Close()
		return nil, nil, withDiagnostics(stream, err)
	}
	descriptors, err := client.ListTools(dialCtx)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, descriptors, nil
}

func newTransport(cfg ServerConfig) (messageStream, error) {
	switch cfg.Transport {
	case TransportStdio, "":
		return newStdioTransport(StdioConfig{Command: cfg.Command, Args: cfg.Args, Env: cfg.Env, Dir: cfg.Dir})
	case TransportHTTP:
		return newHTTPTransport(HTTPConfig{URL: cfg.URL, Headers: cfg.Headers})
	default:
		return nil, fmt.Errorf("mcp: unknown transport %q", cfg.Transport)
	}
}

func buildTools(server string, caller toolCaller, descriptors []ToolDescriptor) []tool.Tool {
	out := make([]tool.Tool, 0, len(descriptors))
	for _, d := range descriptors {
		full, ok := toolName(server, d.Name)
		if !ok {
			continue // skip names that violate the provider tool-name rule
		}
		out = append(out, &mcpTool{
			name:        full,
			serverTool:  d.Name,
			description: d.Description,
			schema:      toolSchema(d.InputSchema),
			caller:      caller,
		})
	}
	return out
}

// withDiagnostics enriches a handshake error with the server's early stderr when
// the transport can supply it (stdio), which is where startup failures show up.
func withDiagnostics(stream messageStream, err error) error {
	if d, ok := stream.(interface{ Diagnostics() string }); ok {
		if tail := d.Diagnostics(); tail != "" {
			return fmt.Errorf("%w (server stderr: %s)", err, tail)
		}
	}
	return err
}
