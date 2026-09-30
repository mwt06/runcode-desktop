package desktop

import (
	"context"
	"fmt"

	"github.com/wt68/runcode/internal/browsertool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// openBrowser is also used by Passport/Codex login. The launcher accepts remote
// login URLs; model-supplied URLs must pass browsertool's loopback validation.
func openBrowser(url string) error { return openBrowserContext(context.Background(), url) }

const localBrowserPrompt = `Desktop interaction:
This is a local desktop host with a system-browser opening capability. A hidden/non-interactive Bash console does not mean the host is headless.
- Use open_preview for workspace files. When open_browser is in your tool list, use it for a skill's local HTTP/HTTPS confirmation or live-preview page. It opens the system browser after approval; it cannot click, submit, or read the page for you. Subagents without this tool must return the URL to their parent.
- Start the service and inspect its logs first. Use the actual reported URL/port, not a hardcoded port. If a dependency is missing or startup fails, report/fix that cause within the normal approval rules; do not claim the browser itself failed. Do not install unrelated dependencies.
- Bash foreground commands are capped at 120000 ms, even if a skill asks for 600000. For commands that wait for human confirmation, use run_in_background and BashOutput, inspect both success and failure output, and keep the user informed. Follow the skill's shutdown procedure when finished; do not repeatedly start duplicate servers.
- Opening a URL is not evidence of a rendered page or user consent. Wait for and read the skill's real confirmation result. Never submit/forge it or silently fall back to skipping the confirmation step. If opening fails, explain the error and offer the local URL for the user to open manually.`

// Show the local host and port before approval without leaking URL queries into
// telemetry. Classification, grants and all outer policy guards stay unchanged.
type browserResolver struct{ inner permissions.Resolver }

func (r browserResolver) Resolve(ctx context.Context, req permissions.ResolveRequest) (permissions.Action, error) {
	action, err := r.inner.Resolve(ctx, req)
	if err != nil || req.ToolName != browsertool.Name {
		return action, err
	}
	host, err := browsertool.TargetHost(req.Input)
	if err != nil {
		return action, fmt.Errorf("%w: %w", permissions.ErrInvalidInput, err)
	}
	if action.Metadata == nil {
		action.Metadata = make(map[string]any)
	}
	action.Metadata[permissions.MetadataNetworkHost] = host
	return action, nil
}
