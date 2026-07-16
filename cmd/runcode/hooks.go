package main

import (
	"fmt"
	"strings"

	"github.com/wt68/runcode/engine/hooks"
	"github.com/wt68/runcode/engine/settings"
)

// (newHookRunner moved to internal/engine, which assembles the session.)

// hooksFromConfig converts and validates the user-level hook config into runnable
// hooks. A misconfigured hook is a hard error so the user fixes it rather than
// silently losing a policy hook.
func hooksFromConfig(configs []settings.HookConfig) ([]hooks.Hook, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	out := make([]hooks.Hook, 0, len(configs))
	for i, c := range configs {
		event, err := normalizeHookEvent(c.Event)
		if err != nil {
			return nil, fmt.Errorf("hook %d: %w", i+1, err)
		}
		if len(c.Command) == 0 || strings.TrimSpace(c.Command[0]) == "" {
			return nil, fmt.Errorf("hook %d: command is required", i+1)
		}
		out = append(out, hooks.Hook{
			Event:     event,
			Matcher:   strings.TrimSpace(c.Matcher),
			Command:   c.Command,
			TimeoutMS: c.TimeoutMS,
		})
	}
	return out, nil
}

func normalizeHookEvent(event string) (hooks.Event, error) {
	switch e := hooks.Event(strings.TrimSpace(event)); e {
	case hooks.EventPreToolUse, hooks.EventPostToolUse, hooks.EventUserPromptSubmit,
		hooks.EventStop, hooks.EventSubagentStop, hooks.EventSessionStart,
		hooks.EventSessionEnd, hooks.EventPreCompact:
		return e, nil
	default:
		return "", fmt.Errorf("unknown hook event %q (want PreToolUse, PostToolUse, UserPromptSubmit, Stop, SubagentStop, SessionStart, SessionEnd, or PreCompact)", event)
	}
}
