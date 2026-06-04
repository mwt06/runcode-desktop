package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// slashHandler runs a slash command against the model and returns the updated
// model plus any command to execute. Each handler owns its control flow (e.g.
// guarding against an in-flight turn), so behavior lives with the command.
type slashHandler func(m Model, args []string) (Model, tea.Cmd)

// slashCommand is one registered command: its canonical name, optional aliases,
// a one-line summary for help, and its handler.
type slashCommand struct {
	name    string
	aliases []string
	summary string
	run     slashHandler
}

// slashRegistry holds the available slash commands. Parsing, dispatch, and help
// are all driven from the registry, so adding a command means registering it in
// exactly one place instead of editing a parse switch, a dispatch switch, and a
// hand-written help string.
type slashRegistry struct {
	order  []*slashCommand
	byName map[string]*slashCommand
}

func newSlashRegistry() *slashRegistry {
	return &slashRegistry{byName: map[string]*slashCommand{}}
}

// register adds a command under its name and every alias. A later registration
// for the same key wins, which lets callers override a default command.
func (r *slashRegistry) register(c *slashCommand) {
	if _, exists := r.byName[c.name]; !exists {
		r.order = append(r.order, c)
	}
	r.byName[c.name] = c
	for _, alias := range c.aliases {
		r.byName[alias] = c
	}
}

func (r *slashRegistry) lookup(name string) (*slashCommand, bool) {
	c, ok := r.byName[name]
	return c, ok
}

// matching returns the registered commands whose name starts with prefix, in
// registration order. An empty prefix returns all commands.
func (r *slashRegistry) matching(prefix string) []*slashCommand {
	var out []*slashCommand
	for _, c := range r.order {
		if strings.HasPrefix(c.name, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// parseSlash splits a submitted line into a lowercase command name and its
// args. isSlash is true for any input that begins with '/'; name is empty for a
// bare '/'.
func parseSlash(input string) (name string, args []string, isSlash bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return "", nil, false
	}
	fields := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(fields) == 0 {
		return "", nil, true
	}
	args = fields[1:]
	if len(args) == 0 {
		args = nil // normalize "no args" to nil for a consistent API
	}
	return strings.ToLower(fields[0]), args, true
}

const scrollingHelp = "Input\n" +
	"  Enter            send\n" +
	"  alt+enter/ctrl+j insert a newline\n" +
	"  ↑/↓              recall history (move the cursor in a multi-line input)\n" +
	"\n" +
	"Scrolling\n" +
	"  PgUp/PgDn        scroll half a page\n" +
	"  Home/End         jump to top / bottom\n" +
	"  Wheel            scroll with mouse wheel"

// helpText renders the command list (auto-generated from the registry, names
// aligned) followed by the static input/scrolling reference.
func (r *slashRegistry) helpText() string {
	width := 0
	for _, c := range r.order {
		if len(c.name) > width {
			width = len(c.name)
		}
	}
	var b strings.Builder
	b.WriteString("Commands\n")
	for _, c := range r.order {
		fmt.Fprintf(&b, "  /%-*s  %s\n", width, c.name, c.summary)
	}
	b.WriteString("\n")
	b.WriteString(scrollingHelp)
	return b.String()
}

// defaultSlashRegistry builds the registry of built-in commands. New commands
// (e.g. /compact, /model, /cost) are added here.
func defaultSlashRegistry() *slashRegistry {
	r := newSlashRegistry()

	r.register(&slashCommand{
		name:    "help",
		summary: "show commands",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			m.appendMessage(RoleSystem, m.commands.helpText())
			m.refreshViewport()
			return m, nil
		},
	})

	r.register(&slashCommand{
		name:    "clear",
		summary: "clear in-memory history and the screen",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			if m.inFlight {
				m.appendMessage(RoleSystem, "cannot clear while assistant is responding")
				m.refreshViewport()
				return m, nil
			}
			return m, resetCmd(m.service)
		},
	})

	r.register(&slashCommand{
		name:    "compact",
		summary: "summarize older context now to free up tokens",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			if m.inFlight {
				m.appendMessage(RoleSystem, "cannot compact while assistant is responding")
				m.refreshViewport()
				return m, nil
			}
			m.appendMessage(RoleSystem, "compacting context…")
			m.refreshViewport()
			return m, compactCmd(m.service)
		},
	})

	r.register(&slashCommand{
		name:    "status",
		summary: "show current session status",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			m.appendMessage(RoleSystem, statusText(m.status, m.state(), m.turnCount, len(m.messages), m.totalInputTokens, m.totalOutputTokens, m.lastReasoningScenario, m.lastReasoningConfidence))
			m.refreshViewport()
			return m, nil
		},
	})

	r.register(&slashCommand{
		name:    "mode",
		summary: "switch permission mode: /mode safe|interactive",
		run: func(m Model, args []string) (Model, tea.Cmd) {
			if len(args) == 0 {
				m.appendMessage(RoleSystem, "usage: /mode safe|interactive (current: "+m.status.PermissionMode+")")
				m.refreshViewport()
				return m, nil
			}
			mode := strings.ToLower(strings.TrimSpace(args[0]))
			if mode != "safe" && mode != "interactive" {
				m.appendMessage(RoleError, "unknown mode "+args[0]+" (want safe or interactive)")
				m.refreshViewport()
				return m, nil
			}
			if err := m.service.SetPermissionMode(mode); err != nil {
				m.appendMessage(RoleError, errorText(err))
				m.refreshViewport()
				return m, nil
			}
			m.status.PermissionMode = mode
			m.appendMessage(RoleSystem, "permission mode: "+mode)
			m.refreshViewport()
			return m, nil
		},
	})

	r.register(&slashCommand{
		name:    "model",
		summary: "switch the model for new turns: /model <name>",
		run: func(m Model, args []string) (Model, tea.Cmd) {
			if len(args) == 0 {
				m.appendMessage(RoleSystem, "usage: /model <name> (current: "+modelLabel(m.status.Model)+")")
				m.refreshViewport()
				return m, nil
			}
			if m.inFlight {
				m.appendMessage(RoleSystem, "cannot switch model while assistant is responding")
				m.refreshViewport()
				return m, nil
			}
			model := strings.TrimSpace(strings.Join(args, " "))
			if err := m.service.SetModel(model); err != nil {
				m.appendMessage(RoleError, errorText(err))
				m.refreshViewport()
				return m, nil
			}
			m.status.Model = model
			m.appendMessage(RoleSystem, "model: "+model)
			m.refreshViewport()
			return m, nil
		},
	})

	r.register(&slashCommand{
		name:    "cost",
		summary: "show token usage and estimated cost",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			m.appendMessage(RoleSystem, costText(m.totalInputTokens, m.totalOutputTokens, m.status.InputPricePerMTok, m.status.OutputPricePerMTok))
			m.refreshViewport()
			return m, nil
		},
	})

	r.register(&slashCommand{
		name:    "exit",
		aliases: []string{"quit"},
		summary: "quit",
		run: func(m Model, _ []string) (Model, tea.Cmd) {
			if m.inFlight && m.turnCancel != nil {
				m.exitingAfterCancel = true
				m.turnCancel()
				return m, nil
			}
			return m, tea.Quit
		},
	})

	return r
}

// costText renders cumulative token usage and, when prices are configured, an
// estimated cost. Prices are per million tokens.
func costText(inputTokens int, outputTokens int, inputPrice float64, outputPrice float64) string {
	var b strings.Builder
	b.WriteString("Session usage\n")
	fmt.Fprintf(&b, "  input tokens:  %d\n", inputTokens)
	fmt.Fprintf(&b, "  output tokens: %d\n", outputTokens)
	fmt.Fprintf(&b, "  total tokens:  %d", inputTokens+outputTokens)
	if inputPrice > 0 || outputPrice > 0 {
		cost := float64(inputTokens)/1e6*inputPrice + float64(outputTokens)/1e6*outputPrice
		fmt.Fprintf(&b, "\n  estimated cost: $%.4f (in $%.2f/Mtok, out $%.2f/Mtok)", cost, inputPrice, outputPrice)
	} else {
		b.WriteString("\n  (set input_price / output_price per million tokens to estimate cost)")
	}
	return b.String()
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
