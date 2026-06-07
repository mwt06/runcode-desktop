package main

import (
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/hooks"
	"github.com/wt68/runcode/internal/persistence/settings"
)

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
	case hooks.EventPreToolUse, hooks.EventPostToolUse, hooks.EventUserPromptSubmit:
		return e, nil
	default:
		return "", fmt.Errorf("unknown hook event %q (want PreToolUse, PostToolUse, or UserPromptSubmit)", event)
	}
}

// newHookRunner builds the hook runner for a session. Infrastructure failures are
// surfaced as warnings on the runtime's stderr (hooks fail open).
func newHookRunner(hookList []hooks.Hook, runtime chatIO) hooks.Runner {
	if len(hookList) == 0 {
		return hooks.Noop{}
	}
	warn := func(event hooks.Event, err error) {
		if runtime.Err != nil {
			fmt.Fprintf(runtime.Err, "warning: %s hook failed to run: %v\n", event, err)
		}
	}
	return hooks.NewRunner(hookList, hooks.Options{Warn: warn})
}
