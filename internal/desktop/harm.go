package desktop

import (
	"context"
	"strings"

	"github.com/wt68/runcode/internal/permissions"
)

// modelHarmJudge implements permissions.HarmJudge by asking the active session's
// model whether an action is harmful. It holds the App (not a fixed session) so
// it always uses the current session, which is built after the permission service.
type modelHarmJudge struct {
	app *App
}

// Assess describes the action in plain language and asks the model to judge it.
// A missing session or model error is returned as an error so the authorizer
// fails safe (falls through to prompting the user).
func (j modelHarmJudge) Assess(ctx context.Context, action permissions.Action) (permissions.HarmVerdict, error) {
	j.app.mu.Lock()
	session := j.app.session
	j.app.mu.Unlock()
	if session == nil {
		return permissions.HarmVerdict{}, errNoSession
	}
	harmful, reason, err := session.AssessHarm(ctx, describeAction(action))
	if err != nil {
		return permissions.HarmVerdict{}, err
	}
	return permissions.HarmVerdict{Harmful: harmful, Reason: reason}, nil
}

// describeAction renders an action as a short instruction for the harm judge. The
// raw command/path is in-process only (the judge runs the user's own model).
func describeAction(action permissions.Action) string {
	switch action.Operation {
	case permissions.OperationExecute:
		return "Run this shell command: " + resourcePath(action, permissions.ResourceCommand)
	case permissions.OperationWrite:
		return "Create or overwrite this file: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationEdit:
		return "Edit this file: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationDelete:
		return "Delete this file or directory: " + resourcePath(action, permissions.ResourceFile)
	case permissions.OperationNetwork:
		host := metaString(action, permissions.MetadataNetworkHost)
		return "Make a network request to host: " + host
	case permissions.OperationExternal:
		server := metaString(action, permissions.MetadataMCPServer)
		tool := metaString(action, permissions.MetadataMCPTool)
		return "Call external MCP tool " + server + "/" + tool
	default:
		return "Tool action: " + action.ToolName
	}
}

func resourcePath(action permissions.Action, kind permissions.ResourceType) string {
	for _, r := range action.Resources {
		if r.Type == kind && strings.TrimSpace(r.Path) != "" {
			return r.Path
		}
	}
	return action.ToolName
}

func metaString(action permissions.Action, key string) string {
	if v, ok := action.Metadata[key].(string); ok {
		return v
	}
	return ""
}
