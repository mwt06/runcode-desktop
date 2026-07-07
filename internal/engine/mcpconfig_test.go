package engine

import (
	"testing"

	"github.com/wt68/runcode/internal/mcp"
	"github.com/wt68/runcode/internal/persistence/settings"
)

func boolPtr(b bool) *bool { return &b }

func TestMCPServersFromConfigStdio(t *testing.T) {
	t.Setenv("MCP_TEST_SECRET", "s3cr3t")
	cfg := settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{
		"fs": {
			Command: "npx",
			Args:    []string{"-y", "${MCP_TEST_SECRET}"},
			Env:     map[string]string{"TOKEN": "${MCP_TEST_SECRET}"},
		},
	}}
	servers, err := MCPServersFromConfig(cfg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers = %#v, want one", servers)
	}
	s := servers[0]
	if s.Name != "fs" || s.Transport != mcp.TransportStdio || s.Command != "npx" {
		t.Fatalf("server = %#v", s)
	}
	if len(s.Args) != 2 || s.Args[1] != "s3cr3t" {
		t.Fatalf("args = %#v, want ${VAR} expanded", s.Args)
	}
	if len(s.Env) != 1 || s.Env[0] != "TOKEN=s3cr3t" {
		t.Fatalf("env = %#v, want TOKEN=s3cr3t", s.Env)
	}
}

func TestMCPServersFromConfigHTTP(t *testing.T) {
	cfg := settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{
		"remote": {Transport: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
	}}
	servers, err := MCPServersFromConfig(cfg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if servers[0].Transport != mcp.TransportHTTP || servers[0].URL != "https://example.com/mcp" || servers[0].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("server = %#v", servers[0])
	}
}

func TestMCPServersFromConfigDisabledSkipped(t *testing.T) {
	cfg := settings.MCPConfig{Servers: map[string]settings.MCPServerConfig{
		"off": {Command: "x", Enabled: boolPtr(false)},
		"on":  {Command: "y"},
	}}
	servers, err := MCPServersFromConfig(cfg)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(servers) != 1 || servers[0].Name != "on" {
		t.Fatalf("servers = %#v, want only the enabled one", servers)
	}
}

func TestMCPServersFromConfigValidation(t *testing.T) {
	cases := map[string]settings.MCPConfig{
		"bad name":              {Servers: map[string]settings.MCPServerConfig{"a__b": {Command: "x"}}},
		"stdio without command": {Servers: map[string]settings.MCPServerConfig{"s": {}}},
		"http without url":      {Servers: map[string]settings.MCPServerConfig{"s": {Transport: "http"}}},
		"unknown transport":     {Servers: map[string]settings.MCPServerConfig{"s": {Transport: "carrier-pigeon"}}},
	}
	for name, cfg := range cases {
		if _, err := MCPServersFromConfig(cfg); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestMCPServersFromConfigEmpty(t *testing.T) {
	servers, err := MCPServersFromConfig(settings.MCPConfig{})
	if err != nil || servers != nil {
		t.Fatalf("empty = %#v, %v, want nil, nil", servers, err)
	}
}
