package permissions

import "context"

// HarmVerdict is a model's assessment of whether an action is harmful.
type HarmVerdict struct {
	// Harmful is true when the action is judged destructive, dangerous, or
	// malicious (as opposed to a routine development action).
	Harmful bool
	// Reason is a short human-readable explanation, shown to the user when the
	// action is escalated to approval.
	Reason string
}

// HarmJudge assesses an action's harmfulness, typically by asking a model. It is
// an optional gate the InteractiveAuthorizer consults before prompting: a "safe"
// verdict auto-allows the action (no prompt), a "harmful" verdict (or a judge
// error) falls through to the user approval prompt. It must be safe for
// concurrent use — actions may be authorized from parallel goroutines.
type HarmJudge interface {
	Assess(ctx context.Context, action Action) (HarmVerdict, error)
}
