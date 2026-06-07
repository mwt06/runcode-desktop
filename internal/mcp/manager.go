package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/wt68/runcode/pkg/tool"
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

type serverConn struct {
	name              string
	client            *Client
	tools             []tool.Tool
	supportsResources bool
	supportsPrompts   bool
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
		for _, t := range conn.tools {
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

// Close stops every connected server. It returns the first close error, if any,
// after attempting to close all of them.
func (m *Manager) Close(context.Context) error {
	if m == nil {
		return nil
	}
	var firstErr error
	for _, c := range m.conns {
		if err := c.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// dialServer is the production dialer: it builds the transport, performs the
// handshake, lists tools, and adapts them. roots are advertised to the server,
// and the sampler (when set) lets the server request model completions.
func dialServer(ctx context.Context, cfg ServerConfig, roots []Root, sampler Sampler) (*serverConn, error) {
	stream, err := newTransport(cfg)
	if err != nil {
		return nil, err
	}
	client := newClientWith(stream, clientConfig{roots: roots, sampler: sampler, serverName: cfg.Name})
	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	if err := client.Initialize(dialCtx); err != nil {
		_ = client.Close()
		return nil, withDiagnostics(stream, err)
	}
	descriptors, err := client.ListTools(dialCtx)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return &serverConn{
		name:              cfg.Name,
		client:            client,
		tools:             buildTools(cfg.Name, client, descriptors),
		supportsResources: client.SupportsResources(),
		supportsPrompts:   client.SupportsPrompts(),
	}, nil
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

func buildTools(server string, client *Client, descriptors []ToolDescriptor) []tool.Tool {
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
			client:      client,
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
