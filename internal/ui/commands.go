package ui

import (
	"fmt"
	"strings"
)

type slashCommand string

const (
	slashNone    slashCommand = ""
	slashHelp    slashCommand = "help"
	slashClear   slashCommand = "clear"
	slashStatus  slashCommand = "status"
	slashExit    slashCommand = "exit"
	slashUnknown slashCommand = "unknown"
)

func parseSlashCommand(input string) slashCommand {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return slashNone
	}
	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return slashUnknown
	}
	name := strings.ToLower(fields[0])
	switch name {
	case "help":
		return slashHelp
	case "clear":
		return slashClear
	case "status":
		return slashStatus
	case "exit", "quit":
		return slashExit
	default:
		return slashUnknown
	}
}

func helpText() string {
	return strings.Join([]string{
		"Commands",
		"  /help     show commands",
		"  /clear    clear in-memory history and the screen",
		"  /status   show current session status",
		"  /exit     quit",
		"",
		"Scrolling",
		"  ↑/↓       scroll one line",
		"  PgUp/PgDn scroll half a page",
		"  Wheel     scroll with mouse wheel",
	}, "\n")
}

func statusText(status Status, state string, turnCount int, messageCount int, inputTokens int, outputTokens int, reasoningScenario string, reasoningConfidence string) string {
	transcript := status.Transcript
	if transcript == "" {
		transcript = "off"
	}
	session := status.SessionID
	if session == "" {
		session = "-"
	}
	git := status.GitBranch
	if git == "" {
		git = "-"
	}
	diff := formatDiffStats(status.GitDiff)
	if diff == "" {
		diff = "-"
	}
	return fmt.Sprintf("model: %s\ncwd: %s\npermission: %s\ntranscript: %s\nsession: %s\nctx: %s\nthink: %s\ngit: %s\ndiff: %s\nstate: %s\nturns: %d\nmessages: %d",
		status.Model,
		status.CWD,
		status.PermissionMode,
		transcript,
		session,
		formatContext(inputTokens, outputTokens, status.MaxContextTokens),
		formatThinking(status.ThinkingMode, reasoningScenario, reasoningConfidence),
		git,
		diff,
		state,
		turnCount,
		messageCount,
	)
}
