package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/mcp"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "config",
		Short:        "Show the effective configuration and where it is loaded from",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, resolved, err := resolveChatConfig(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "Effective configuration (flag > env > project file > user file > default):")
			fmt.Fprintf(out, "  provider:             %s\n", configValueOrUnset(cfg.Provider))
			fmt.Fprintf(out, "  model:                %s\n", configValueOrUnset(cfg.Model))
			fmt.Fprintf(out, "  base_url:             %s\n", configValueOrUnset(cfg.BaseURL))
			fmt.Fprintf(out, "  max_tokens:           %d\n", cfg.MaxTokens)
			fmt.Fprintf(out, "  permission_mode:      %s\n", cfg.PermissionMode)
			fmt.Fprintf(out, "  telemetry:            %s\n", cfg.Telemetry)
			fmt.Fprintf(out, "  transcript:           %s\n", cfg.Transcript)
			fmt.Fprintf(out, "  max_history_messages: %d\n", cfg.MaxHistoryMessages)
			fmt.Fprintf(out, "  max_context_tokens:   %d\n", cfg.MaxContextTokens)
			fmt.Fprintf(out, "  max_retries:          %d\n", cfg.MaxRetries)
			fmt.Fprintf(out, "  input_price:          %g\n", cfg.InputPrice)
			fmt.Fprintf(out, "  output_price:         %g\n", cfg.OutputPrice)
			fmt.Fprintf(out, "  cwd:                  %s\n", cfg.CWD)
			fmt.Fprintf(out, "  mcp_servers:          %s\n", mcpServerSummary(cfg.MCPServers))
			fmt.Fprintf(out, "  credentials:          %s\n", credentialStatus(cfg))
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Config files:")
			fmt.Fprintf(out, "  project: %s\n", configPathOrNone(resolved.ProjectPath))
			fmt.Fprintf(out, "  user:    %s\n", configPathOrNone(resolved.UserPath))
			return nil
		},
	}
	addChatConfigFlags(cmd)
	return cmd
}

// mcpServerSummary lists the enabled MCP server names and transports without
// printing commands, URLs, env, or headers (which may contain secrets).
func mcpServerSummary(servers []mcp.ServerConfig) string {
	if len(servers) == 0 {
		return "<none>"
	}
	parts := make([]string, len(servers))
	for i, s := range servers {
		parts[i] = fmt.Sprintf("%s (%s)", s.Name, s.Transport)
	}
	return strings.Join(parts, ", ")
}

func configValueOrUnset(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<unset>"
	}
	return value
}

func configPathOrNone(path string) string {
	if path == "" {
		return "<none>"
	}
	return path
}

// credentialStatus reports whether a credential is configured without ever
// printing its value.
func credentialStatus(cfg chatConfig) string {
	switch {
	case cfg.AuthToken != "":
		return "auth-token set"
	case cfg.APIKey != "":
		return "api-key set"
	default:
		return "<unset>"
	}
}
