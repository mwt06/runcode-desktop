package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

// ListResourcesToolName and ReadResourceToolName are the model-facing names of
// the built-in MCP resource tools. Unlike server tools they are not namespaced
// per server (the server is an argument), so the permission layer recognizes
// them by exact name and classifies them as external operations.
const (
	ListResourcesToolName = "ListMcpResources"
	ReadResourceToolName  = "ReadMcpResource"
)

// resourceServers holds the resource-capable servers by name so the resource
// tools can route a request to the right connection. Only servers that advertised
// the resources capability are included. Routing to the serverConn (not a bare
// client) means a reconnect after a dropped connection is transparent here too.
type resourceServers struct {
	names   []string
	servers map[string]*serverConn
}

func newResourceServers(conns []*serverConn) *resourceServers {
	rs := &resourceServers{servers: make(map[string]*serverConn)}
	for _, c := range conns {
		if c == nil || !c.supportsResources || c.client == nil {
			continue
		}
		if _, dup := rs.servers[c.name]; dup {
			continue // a duplicate server name cannot be routed uniquely; keep the first
		}
		rs.servers[c.name] = c
		rs.names = append(rs.names, c.name)
	}
	sort.Strings(rs.names)
	return rs
}

func (rs *resourceServers) empty() bool { return len(rs.names) == 0 }

// tools returns the built-in list/read resource tools backed by these servers.
func (rs *resourceServers) tools() []tool.Tool {
	return []tool.Tool{&listResourcesTool{servers: rs}, &readResourceTool{servers: rs}}
}

// listResourcesTool lists resources across the resource-capable servers.
type listResourcesTool struct{ servers *resourceServers }

func (t *listResourcesTool) Name() string { return ListResourcesToolName }

func (t *listResourcesTool) Description() string {
	return "List resources exposed by connected MCP servers. Optionally pass a server name to list only that server. Returns each resource's server, uri, name, and description; use ReadMcpResource to fetch the contents of one."
}

func (t *listResourcesTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"server": {Type: tool.SchemaTypeString, Description: "Optional MCP server name to list resources from. Omit to list all servers."},
		},
	}
}

// IsConcurrencySafe is false: the call contacts external servers and is gated by
// approval, which cannot run concurrently.
func (t *listResourcesTool) IsConcurrencySafe() bool { return false }

type listResourcesInput struct {
	Server string `json:"server"`
}

func (t *listResourcesTool) Run(ctx context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in listResourcesInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return resourceErrorResult("invalid ListMcpResources input: expected an object with an optional \"server\" field"), nil
		}
	}
	server := strings.TrimSpace(in.Server)
	var names []string
	switch {
	case server != "":
		if _, ok := t.servers.servers[server]; !ok {
			return resourceErrorResult(fmt.Sprintf("unknown or non-resource MCP server %q", server)), nil
		}
		names = []string{server}
	default:
		names = t.servers.names
	}

	var b strings.Builder
	count := 0
	for _, name := range names {
		client, err := t.servers.servers[name].live(ctx)
		if err != nil {
			fmt.Fprintf(&b, "server %s: unable to list resources\n\n", name)
			continue
		}
		descriptors, err := client.ListResources(ctx)
		if err != nil {
			// Report the failing server without leaking the raw error text.
			fmt.Fprintf(&b, "server %s: unable to list resources\n\n", name)
			continue
		}
		for _, d := range descriptors {
			count++
			fmt.Fprintf(&b, "server: %s\nuri: %s\nname: %s\n", name, d.URI, d.Name)
			if d.Description != "" {
				fmt.Fprintf(&b, "description: %s\n", d.Description)
			}
			if d.MimeType != "" {
				fmt.Fprintf(&b, "mimeType: %s\n", d.MimeType)
			}
			b.WriteString("\n")
		}
	}
	text := strings.TrimSpace(b.String())
	if count == 0 && text == "" {
		text = "(no resources)"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}

// readResourceTool reads one resource's contents by server and uri.
type readResourceTool struct{ servers *resourceServers }

func (t *readResourceTool) Name() string { return ReadResourceToolName }

func (t *readResourceTool) Description() string {
	return "Read the contents of an MCP resource by server and uri. Discover available resources first with ListMcpResources."
}

func (t *readResourceTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"server": {Type: tool.SchemaTypeString, Description: "The MCP server that exposes the resource."},
			"uri":    {Type: tool.SchemaTypeString, Description: "The resource uri, as shown by ListMcpResources."},
		},
		Required: []string{"server", "uri"},
	}
}

func (t *readResourceTool) IsConcurrencySafe() bool { return false }

type readResourceInput struct {
	Server string `json:"server"`
	URI    string `json:"uri"`
}

func (t *readResourceTool) Run(ctx context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in readResourceInput
	if err := json.Unmarshal(input, &in); err != nil {
		return resourceErrorResult("invalid ReadMcpResource input: expected \"server\" and \"uri\""), nil
	}
	server := strings.TrimSpace(in.Server)
	uri := strings.TrimSpace(in.URI)
	if server == "" || uri == "" {
		return resourceErrorResult("ReadMcpResource requires both \"server\" and \"uri\""), nil
	}
	sc, ok := t.servers.servers[server]
	if !ok {
		return resourceErrorResult(fmt.Sprintf("unknown or non-resource MCP server %q", server)), nil
	}
	client, err := sc.live(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	result, err := client.ReadResource(ctx, uri)
	if err != nil {
		// A transport/protocol failure is returned so the executor reports a
		// recoverable is_error result and the model can adapt.
		return tool.Result{}, err
	}
	return mapResourceContents(result), nil
}

// mapResourceContents converts a resources/read result into a runcode tool
// result. Inline text passes through; binary (blob) content is noted as a
// placeholder since it is not inlined into the model context in this increment.
func mapResourceContents(result ReadResourceResult) tool.Result {
	content := make([]tool.ResultContent, 0, len(result.Contents))
	for _, c := range result.Contents {
		if c.Text != "" {
			content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: c.Text})
			continue
		}
		note := "[binary resource omitted"
		if c.MimeType != "" {
			note += ": " + c.MimeType
		}
		note += "]"
		content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: note})
	}
	if len(content) == 0 {
		content = append(content, tool.ResultContent{Type: tool.ResultContentTypeText, Text: "(empty resource)"})
	}
	return tool.Result{Content: content}
}

func resourceErrorResult(msg string) tool.Result {
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}},
		IsError: true,
	}
}
