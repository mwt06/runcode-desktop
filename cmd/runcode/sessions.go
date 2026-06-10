package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/pkg/llm"
)

// Rune caps for `sessions show`, so a single huge message (or tool dump) cannot
// flood the terminal.
const (
	showMaxTextRunes       = 4000
	showMaxToolResultRunes = 600
)

// sessionsCmd browses the saved full-history sessions under
// <workspace>/.runcode/sessions, the same files `--resume` reads. It exists so a
// session id is discoverable rather than something the user has to remember.
func sessionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "sessions",
		Short:        "Browse and inspect saved sessions you can resume",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSessionsList(cmd)
		},
	}
	cmd.PersistentFlags().String("cwd", "", "workspace directory (default: current directory; env RUNCODE_CWD)")
	cmd.AddCommand(sessionsListCmd())
	cmd.AddCommand(sessionsShowCmd())
	return cmd
}

func sessionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List saved sessions, newest first",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSessionsList(cmd)
		},
	}
}

func sessionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "show <id|number>",
		Short:        "Print a saved session's conversation (id, or its number from `sessions list`)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := cwdConfig(cmd)
			if err != nil {
				return err
			}
			id, err := resolveSessionRef(cwd, args[0])
			if err != nil {
				return err
			}
			history, err := sessions.LoadHistory(cwd, id)
			if err != nil {
				return err
			}
			if len(history) == 0 {
				return fmt.Errorf("session %q has no saved history", id)
			}
			renderSessionHistory(cmd.OutOrStdout(), id, history)
			return nil
		},
	}
}

func runSessionsList(cmd *cobra.Command) error {
	cwd, err := cwdConfig(cmd)
	if err != nil {
		return err
	}
	infos, err := sessions.List(cwd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(infos) == 0 {
		fmt.Fprintln(out, "No saved sessions in this workspace.")
		fmt.Fprintln(out, "Sessions are saved automatically; start one with `runcode chat` or `runcode tui`.")
		return nil
	}
	for i, info := range infos {
		preview := info.LastUser
		if preview == "" {
			preview = info.FirstUser
		}
		if preview == "" {
			preview = "(no user text)"
		}
		fmt.Fprintf(out, "[%d] %-22s %-9s %3d turn%s  %s\n",
			i+1, info.ID, humanizeSince(time.Since(info.ModTime)), info.Turns, plural(info.Turns), preview)
	}
	fmt.Fprintln(out, "\nResume with `runcode chat --resume <id>` or `runcode tui --resume <id>`.")
	return nil
}

// resolveSessionRef accepts either a 1-based number from `sessions list` (newest
// first) or a raw session id. A bare id is returned as-is; LoadHistory validates
// it and reports a missing file.
func resolveSessionRef(cwd, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty session reference")
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 {
			return "", fmt.Errorf("invalid session number %q", ref)
		}
		infos, err := sessions.List(cwd)
		if err != nil {
			return "", err
		}
		if n > len(infos) {
			return "", fmt.Errorf("no session numbered %d (there are %d)", n, len(infos))
		}
		return infos[n-1].ID, nil
	}
	return ref, nil
}

// renderSessionHistory prints a readable, bounded rendering of a session: user
// and assistant text, tool calls noted by name, and tool results as a short
// snippet. It is for human inspection, not a loss-less dump.
func renderSessionHistory(w io.Writer, id string, history []llm.Message) {
	fmt.Fprintf(w, "session %s — %d messages\n", id, len(history))
	for _, m := range history {
		fmt.Fprintln(w)
		switch m.Role {
		case llm.RoleUser:
			text := strings.TrimSpace(llm.TextContent(m))
			switch {
			case text != "":
				fmt.Fprintf(w, "user:\n%s\n", truncateRunes(text, showMaxTextRunes))
			case hasBlock(m, llm.ContentBlockTypeToolResult):
				fmt.Fprintf(w, "user (tool results): %s\n", toolResultsSummary(m))
			case hasBlock(m, llm.ContentBlockTypeImage):
				fmt.Fprintln(w, "user: (image)")
			default:
				fmt.Fprintln(w, "user:")
			}
		case llm.RoleAssistant:
			text := strings.TrimSpace(llm.TextContent(m))
			if text != "" {
				fmt.Fprintf(w, "assistant:\n%s\n", truncateRunes(text, showMaxTextRunes))
			}
			for _, block := range m.Content {
				if block.Type == llm.ContentBlockTypeToolUse {
					fmt.Fprintf(w, "  → tool %s\n", block.Name)
				}
			}
			if text == "" && !hasBlock(m, llm.ContentBlockTypeToolUse) {
				fmt.Fprintln(w, "assistant:")
			}
		case llm.RoleTool:
			fmt.Fprintf(w, "tool result: %s\n", toolResultsSummary(m))
		default:
			// system or other roles are not part of a saved conversation; skip.
		}
	}
}

// toolResultsSummary joins the text of a message's tool_result blocks into one
// bounded snippet.
func toolResultsSummary(m llm.Message) string {
	var b strings.Builder
	for _, block := range m.Content {
		if block.Type != llm.ContentBlockTypeToolResult {
			continue
		}
		for _, inner := range block.Content {
			if inner.Type == llm.ContentBlockTypeText {
				b.WriteString(inner.Text)
				b.WriteString(" ")
			}
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	if s == "" {
		return "(no text)"
	}
	return truncateRunes(s, showMaxToolResultRunes)
}

func hasBlock(m llm.Message, t llm.ContentBlockType) bool {
	for _, block := range m.Content {
		if block.Type == t {
			return true
		}
	}
	return false
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// humanizeSince renders an age as a compact relative label (just now, 5m ago, 3h
// ago, 2d ago).
func humanizeSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
