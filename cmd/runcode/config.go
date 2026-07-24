package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/agent"
	"gitlab.ouc-online.com.cn/aibase/agentloop/hooks"
	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/skill"
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
			_, _ = fmt.Fprintln(out, "Effective configuration (flag > env > project file > user file > default):")
			_, _ = fmt.Fprintf(out, "  provider:             %s\n", configValueOrUnset(cfg.Provider))
			_, _ = fmt.Fprintf(out, "  model:                %s\n", configValueOrUnset(cfg.Model))
			_, _ = fmt.Fprintf(out, "  base_url:             %s\n", configValueOrUnset(cfg.BaseURL))
			_, _ = fmt.Fprintf(out, "  max_tokens:           %d\n", cfg.MaxTokens)
			_, _ = fmt.Fprintf(out, "  permission_mode:      %s\n", cfg.PermissionMode)
			_, _ = fmt.Fprintf(out, "  telemetry:            %s\n", cfg.Telemetry)
			_, _ = fmt.Fprintf(out, "  transcript:           %s\n", cfg.Transcript)
			_, _ = fmt.Fprintf(out, "  max_history_messages: %d\n", cfg.MaxHistoryMessages)
			_, _ = fmt.Fprintf(out, "  max_context_tokens:   %d\n", cfg.MaxContextTokens)
			_, _ = fmt.Fprintf(out, "  max_retries:          %d\n", cfg.MaxRetries)
			_, _ = fmt.Fprintf(out, "  input_price:          %g%s\n", cfg.InputPrice, priceSourceNote(cfg))
			_, _ = fmt.Fprintf(out, "  output_price:         %g\n", cfg.OutputPrice)
			_, _ = fmt.Fprintf(out, "  cwd:                  %s\n", cfg.CWD)
			_, _ = fmt.Fprintf(out, "  mcp_servers:          %s\n", mcpServerSummary(cfg.MCPServers))
			_, _ = fmt.Fprintf(out, "  mcp_sampling:         %s\n", mcpSamplingSummary(cfg))
			_, _ = fmt.Fprintf(out, "  skills:               %s\n", skillSummary(cfg.CWD))
			_, _ = fmt.Fprintf(out, "  agents:               %s\n", agentSummary(cfg.CWD))
			_, _ = fmt.Fprintf(out, "  memory:               %s\n", memorySummary(cfg.CWD))
			_, _ = fmt.Fprintf(out, "  hooks:                %s\n", hookSummary(cfg.Hooks))
			_, _ = fmt.Fprintf(out, "  credentials:          %s\n", credentialStatus(cfg))
			_, _ = fmt.Fprintln(out)
			_, _ = fmt.Fprintln(out, "Config files:")
			_, _ = fmt.Fprintf(out, "  project: %s\n", configPathOrNone(resolved.ProjectPath))
			_, _ = fmt.Fprintf(out, "  user:    %s\n", configPathOrNone(resolved.UserPath))
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

// hookSummary lists the configured hooks as "event:matcher" without printing the
// commands they run.
func hookSummary(hookList []hooks.Hook) string {
	if len(hookList) == 0 {
		return "<none>"
	}
	parts := make([]string, len(hookList))
	for i, h := range hookList {
		matcher := h.Matcher
		if matcher == "" {
			matcher = "*"
		}
		parts[i] = fmt.Sprintf("%s:%s", h.Event, matcher)
	}
	return strings.Join(parts, ", ")
}

// mcpSamplingSummary reports whether MCP servers may request model completions,
// reflecting that safe mode always refuses regardless of the opt-in.
func mcpSamplingSummary(cfg chatConfig) string {
	switch {
	case !cfg.AllowMCPSampling:
		return "off"
	case cfg.PermissionMode == "safe":
		return "off (denied in safe mode)"
	default:
		return "on"
	}
}

// priceSourceNote annotates the effective pricing with where it came from, so a
// built-in default is not mistaken for a user-configured price.
func priceSourceNote(cfg chatConfig) string {
	switch cfg.PriceSource {
	case "builtin":
		return "  (built-in pricing for " + cfg.Model + ")"
	case "explicit":
		return "  (explicit)"
	default:
		return "  (unset — no built-in price for this model)"
	}
}

// skillSummary lists the loaded skill names (tagging project-sourced ones)
// without printing any skill body. Loading problems are ignored here; they are
// surfaced as warnings when a session starts.
func skillSummary(cwd string) string {
	set, _ := engine.LoadSkills(cwd, userConfigDir())
	if set.Len() == 0 {
		return "<none>"
	}
	parts := make([]string, 0, set.Len())
	for _, sk := range set.All() {
		label := sk.Name
		if sk.Source == skill.SourceProject {
			label += " (project)"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, ", ")
}

// agentSummary lists the available sub-agent names (tagging user/project-sourced
// ones) without printing any prompt body. The built-in general-purpose agent is
// always present, so this never reports <none>. Loading problems are ignored here;
// they are surfaced as warnings when a session starts.
func agentSummary(cwd string) string {
	set, _ := engine.LoadAgents(cwd, userConfigDir())
	if set.Len() == 0 {
		return "<none>"
	}
	parts := make([]string, 0, set.Len())
	for _, a := range set.All() {
		label := a.Name
		switch a.Source {
		case agent.SourceProject:
			label += " (project)"
		case agent.SourceUser:
			label += " (user)"
		}
		parts = append(parts, label)
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
