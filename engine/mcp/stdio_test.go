package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestStdioHelperProcess is not a real test: when MCP_STDIO_HELPER=1 it acts as a
// minimal MCP server over stdin/stdout so TestStdioTransportRoundTrip can exercise
// the real subprocess wiring cross-platform by re-executing this test binary.
func TestStdioHelperProcess(t *testing.T) {
	if os.Getenv("MCP_STDIO_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			respondStdioHelper(line)
		}
		if err != nil {
			break
		}
	}
	os.Exit(0)
}

func respondStdioHelper(line string) {
	var msg rpcMessage
	if json.Unmarshal([]byte(line), &msg) != nil || msg.ID == nil {
		return // ignore malformed frames and notifications
	}
	var result any
	switch msg.Method {
	case "initialize":
		init := initializeResult{ProtocolVersion: protocolVersion, ServerInfo: ServerInfo{Name: "stdio-fake", Version: "0.1"}}
		// Advertise resources/prompts only when asked, so tests that do not expect
		// those tools (the default) keep a stable tool set.
		if os.Getenv("MCP_STDIO_RESOURCES") == "1" {
			init.Capabilities.Resources = &resourceCapability{}
		}
		if os.Getenv("MCP_STDIO_PROMPTS") == "1" {
			init.Capabilities.Prompts = &promptCapability{}
		}
		result = init
	case "tools/list":
		result = listToolsResult{Tools: []ToolDescriptor{{Name: "ping", Description: "ping"}}}
	case "tools/call":
		result = ToolResult{Content: []Content{{Type: "text", Text: "pong"}}}
	case "resources/list":
		result = listResourcesResult{Resources: []ResourceDescriptor{{URI: "file:///hello", Name: "hello", Description: "greeting"}}}
	case "resources/read":
		result = ReadResourceResult{Contents: []ResourceContents{{URI: "file:///hello", MimeType: "text/plain", Text: "hi there"}}}
	case "prompts/list":
		result = listPromptsResult{Prompts: []PromptDescriptor{{Name: "greet", Description: "greeting"}}}
	case "prompts/get":
		result = GetPromptResult{Messages: []PromptMessage{{Role: "user", Content: Content{Type: "text", Text: "say hello"}}}}
	default:
		result = struct{}{}
	}
	resp := rpcMessage{JSONRPC: jsonRPCVersion, ID: msg.ID}
	raw, _ := json.Marshal(result)
	resp.Result = raw
	out, _ := json.Marshal(resp)
	os.Stdout.Write(append(out, '\n'))
}

func TestStdioTransportRoundTrip(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	stream, err := newStdioTransport(StdioConfig{
		Command: exe,
		Args:    []string{"-test.run=TestStdioHelperProcess"},
		Env:     []string{"MCP_STDIO_HELPER=1"},
	})
	if err != nil {
		t.Fatalf("newStdioTransport: %v", err)
	}
	client := newClient(stream)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v (stderr: %s)", err, stream.Diagnostics())
	}
	if got := client.ServerInfo().Name; got != "stdio-fake" {
		t.Fatalf("server name = %q, want stdio-fake", got)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools = %#v, want one ping tool", tools)
	}
	result, err := client.CallTool(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "pong" {
		t.Fatalf("result = %#v, want pong", result)
	}
}

func TestStdioTransportMissingCommand(t *testing.T) {
	if _, err := newStdioTransport(StdioConfig{}); err == nil {
		t.Fatal("expected error for empty command")
	}
	if _, err := newStdioTransport(StdioConfig{Command: "definitely-not-a-real-binary-xyz"}); err == nil {
		t.Fatal("expected error starting a nonexistent binary")
	}
}
