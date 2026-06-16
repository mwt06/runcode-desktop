package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wt68/runcode/pkg/tool"
)

// ListPromptsToolName and GetPromptToolName are the model-facing names of the
// built-in MCP prompt tools. Like the resource tools, the server is an argument
// rather than a name prefix, so the permission layer matches them by exact name
// and classifies them as external operations.
const (
	ListPromptsToolName = "ListMcpPrompts"
	GetPromptToolName   = "GetMcpPrompt"
)

// promptServers holds the prompt-capable servers by name so the prompt tools can
// route a request to the right connection. Routing to the serverConn (not a bare
// client) keeps a reconnect after a dropped connection transparent here too.
type promptServers struct {
	names   []string
	servers map[string]*serverConn
}

func newPromptServers(conns []*serverConn) *promptServers {
	ps := &promptServers{servers: make(map[string]*serverConn)}
	for _, c := range conns {
		if c == nil || !c.supportsPrompts || c.client == nil {
			continue
		}
		if _, dup := ps.servers[c.name]; dup {
			continue
		}
		ps.servers[c.name] = c
		ps.names = append(ps.names, c.name)
	}
	sort.Strings(ps.names)
	return ps
}

func (ps *promptServers) empty() bool { return len(ps.names) == 0 }

func (ps *promptServers) tools() []tool.Tool {
	return []tool.Tool{&listPromptsTool{servers: ps}, &getPromptTool{servers: ps}}
}

// listPromptsTool lists prompt templates across the prompt-capable servers.
type listPromptsTool struct{ servers *promptServers }

func (t *listPromptsTool) Name() string { return ListPromptsToolName }

func (t *listPromptsTool) Description() string {
	return "List prompt templates exposed by connected MCP servers. Optionally pass a server name to list only that server. Returns each prompt's server, name, description, and arguments; use GetMcpPrompt to render one."
}

func (t *listPromptsTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"server": {Type: tool.SchemaTypeString, Description: "Optional MCP server name to list prompts from. Omit to list all servers."},
		},
	}
}

func (t *listPromptsTool) IsConcurrencySafe() bool { return false }

type listPromptsInput struct {
	Server string `json:"server"`
}

func (t *listPromptsTool) Run(ctx context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in listPromptsInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return promptErrorResult("invalid ListMcpPrompts input: expected an object with an optional \"server\" field"), nil
		}
	}
	server := strings.TrimSpace(in.Server)
	var names []string
	switch {
	case server != "":
		if _, ok := t.servers.servers[server]; !ok {
			return promptErrorResult(fmt.Sprintf("unknown or non-prompt MCP server %q", server)), nil
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
			fmt.Fprintf(&b, "server %s: unable to list prompts\n\n", name)
			continue
		}
		prompts, err := client.ListPrompts(ctx)
		if err != nil {
			fmt.Fprintf(&b, "server %s: unable to list prompts\n\n", name)
			continue
		}
		for _, p := range prompts {
			count++
			fmt.Fprintf(&b, "server: %s\nname: %s\n", name, p.Name)
			if p.Description != "" {
				fmt.Fprintf(&b, "description: %s\n", p.Description)
			}
			if len(p.Arguments) > 0 {
				fmt.Fprintf(&b, "arguments: %s\n", formatPromptArgs(p.Arguments))
			}
			b.WriteString("\n")
		}
	}
	text := strings.TrimSpace(b.String())
	if count == 0 && text == "" {
		text = "(no prompts)"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}

func formatPromptArgs(args []PromptArgument) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.Name
		if a.Required {
			parts[i] += " (required)"
		}
	}
	return strings.Join(parts, ", ")
}

// getPromptTool renders one prompt template by server and name.
type getPromptTool struct{ servers *promptServers }

func (t *getPromptTool) Name() string { return GetPromptToolName }

func (t *getPromptTool) Description() string {
	return "Render an MCP prompt template by server and name, returning its messages. Discover available prompts first with ListMcpPrompts; pass any required arguments as a string map."
}

func (t *getPromptTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"server":    {Type: tool.SchemaTypeString, Description: "The MCP server that exposes the prompt."},
			"name":      {Type: tool.SchemaTypeString, Description: "The prompt name, as shown by ListMcpPrompts."},
			"arguments": {Type: tool.SchemaTypeObject, Description: "Optional string arguments for the prompt template."},
		},
		Required: []string{"server", "name"},
	}
}

func (t *getPromptTool) IsConcurrencySafe() bool { return false }

type getPromptInput struct {
	Server    string            `json:"server"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

func (t *getPromptTool) Run(ctx context.Context, input json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in getPromptInput
	if err := json.Unmarshal(input, &in); err != nil {
		return promptErrorResult("invalid GetMcpPrompt input: expected \"server\" and \"name\""), nil
	}
	server := strings.TrimSpace(in.Server)
	name := strings.TrimSpace(in.Name)
	if server == "" || name == "" {
		return promptErrorResult("GetMcpPrompt requires both \"server\" and \"name\""), nil
	}
	sc, ok := t.servers.servers[server]
	if !ok {
		return promptErrorResult(fmt.Sprintf("unknown or non-prompt MCP server %q", server)), nil
	}
	client, err := sc.live(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	result, err := client.GetPrompt(ctx, name, in.Arguments)
	if err != nil {
		return tool.Result{}, err
	}
	return mapPromptResult(result), nil
}

// mapPromptResult renders a prompts/get result as text: an optional description
// followed by each message as "role: text". Non-text content is noted as a
// placeholder, consistent with tool and resource results.
func mapPromptResult(result GetPromptResult) tool.Result {
	var b strings.Builder
	if result.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", result.Description)
	}
	for _, m := range result.Messages {
		text := m.Content.Text
		if m.Content.Type != "text" || text == "" {
			note := "[" + m.Content.Type + " content omitted]"
			if m.Content.MimeType != "" {
				note = "[" + m.Content.Type + " content omitted: " + m.Content.MimeType + "]"
			}
			text = note
		}
		fmt.Fprintf(&b, "%s: %s\n", m.Role, text)
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		text = "(empty prompt)"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}
}

func promptErrorResult(msg string) tool.Result {
	return tool.Result{
		Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: msg}},
		IsError: true,
	}
}
