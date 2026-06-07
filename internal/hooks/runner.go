package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// defaultHookTimeout bounds one hook command when the hook sets no timeout.
const defaultHookTimeout = 30 * time.Second

// maxHookFeedbackRunes bounds the hook output surfaced to the model or user.
const maxHookFeedbackRunes = 4000

// execFunc runs a hook command and reports its exit code and combined output. A
// non-nil error means the command could not be run to completion (failed to
// start, timed out) — distinct from a clean run that exited non-zero. It is a
// seam so the runner is unit-tested without spawning processes.
type execFunc func(ctx context.Context, command []string, stdin []byte, timeout time.Duration) (exitCode int, output string, err error)

// Options configures a Runner.
type Options struct {
	// Warn, if set, is called when a hook fails to run (infrastructure failure),
	// which is treated as non-blocking (fail-open) so a broken hook does not brick
	// the session.
	Warn func(event Event, err error)
}

// NewRunner builds a Runner over the given hooks. With no hooks it returns a
// Noop so callers pay nothing.
func NewRunner(hooks []Hook, opts Options) Runner {
	if len(hooks) == 0 {
		return Noop{}
	}
	return &commandRunner{hooks: hooks, exec: runCommand, warn: opts.Warn}
}

type commandRunner struct {
	hooks []Hook
	exec  execFunc
	warn  func(event Event, err error)
}

// Run executes every hook matching the event in order. The first hook that runs
// and exits non-zero blocks (its output is the reason). Hooks that fail to run
// are skipped with a warning (fail-open). Otherwise the outputs of any hooks that
// produced some are concatenated and returned (used as feedback/context).
func (r *commandRunner) Run(ctx context.Context, in Input) Decision {
	var outputs []string
	for _, h := range r.hooks {
		if h.Event != in.Event || !matcherMatches(h.Matcher, in.ToolName) {
			continue
		}
		payload, err := json.Marshal(in)
		if err != nil {
			continue
		}
		timeout := time.Duration(h.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = defaultHookTimeout
		}
		exit, output, err := r.exec(ctx, h.Command, payload, timeout)
		if err != nil {
			// Infrastructure failure: the hook could not be run to completion. Fail
			// open so a broken hook does not brick the session, but make it visible.
			if r.warn != nil {
				r.warn(h.Event, err)
			}
			continue
		}
		output = sanitizeOutput(output)
		if exit != 0 {
			return Decision{Block: true, Output: output}
		}
		if output != "" {
			outputs = append(outputs, output)
		}
	}
	return Decision{Output: strings.Join(outputs, "\n\n")}
}

// matcherMatches reports whether a tool-event matcher applies to a tool name. An
// empty matcher or "*" matches every tool (and every non-tool event, where the
// tool name is empty).
func matcherMatches(matcher string, toolName string) bool {
	matcher = strings.TrimSpace(matcher)
	if matcher == "" || matcher == "*" {
		return true
	}
	return matcher == toolName
}

// sanitizeOutput trims, bounds, and strips control characters (keeping newlines)
// from hook output before it is shown to the model or user.
func sanitizeOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range text {
		if r == '\n' {
			b.WriteRune(r)
			continue
		}
		if r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	runes := []rune(out)
	if len(runes) > maxHookFeedbackRunes {
		out = string(runes[:maxHookFeedbackRunes-1]) + "…"
	}
	return strings.TrimSpace(out)
}
