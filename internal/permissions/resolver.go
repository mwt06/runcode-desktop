package permissions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (Action, error)
}

type ResolveRequest struct {
	ToolName string
	Input    json.RawMessage
	Context  *tool.Context
}

type DefaultResolver struct{}

type readInput struct {
	Path string `json:"path"`
}

type searchInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type writeInput struct {
	Path string `json:"path"`
}

type editInput struct {
	Path string `json:"path"`
}

type bashInput struct {
	Command string `json:"command"`
}

func (DefaultResolver) Resolve(_ context.Context, req ResolveRequest) (Action, error) {
	switch req.ToolName {
	case "Read":
		return resolveRead(req)
	case "Glob", "Grep":
		return resolveSearch(req)
	case "Write":
		return resolveWrite(req)
	case "Edit":
		return resolveEdit(req)
	case "Bash":
		return resolveBash(req)
	case "TodoWrite":
		return Action{ToolName: req.ToolName, Operation: OperationManage, Risk: RiskLow}, nil
	case "WebFetch":
		return resolveWebFetch(req)
	default:
		return Action{ToolName: req.ToolName, Operation: OperationUnknown, Risk: RiskHigh, Resources: []Resource{{Type: ResourceUnknown, Scope: ResourceScopeUnknown}}}, nil
	}
}

func resolveRead(req ResolveRequest) (Action, error) {
	var input readInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return readFallback(req.ToolName), fmt.Errorf("%w: parse read input", ErrInvalidInput)
	}
	if input.Path == "" {
		return readFallback(req.ToolName), fmt.Errorf("%w: path is required", ErrInvalidInput)
	}
	return readActionForPath(req.ToolName, input.Path, req.Context)
}

func resolveSearch(req ResolveRequest) (Action, error) {
	var input searchInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return readFallback(req.ToolName), fmt.Errorf("%w: parse search input", ErrInvalidInput)
	}
	if input.Pattern == "" {
		return readFallback(req.ToolName), fmt.Errorf("%w: pattern is required", ErrInvalidInput)
	}
	return readActionForPath(req.ToolName, input.Path, req.Context)
}

func readActionForPath(toolName string, inputPath string, tctx *tool.Context) (Action, error) {
	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return readFallback(toolName), fmt.Errorf("%w: resolve workspace", ErrInvalidInput)
	}
	path := workspace
	if inputPath != "" {
		path, err = toolpath.Resolve(inputPath, tctx)
		if err != nil {
			return readFallback(toolName), fmt.Errorf("%w: resolve path", ErrInvalidInput)
		}
	}
	return Action{
		ToolName:  toolName,
		Operation: OperationRead,
		Risk:      RiskLow,
		Resources: []Resource{{Type: ResourceFile, Scope: resourceScope(workspace, path), Path: path}},
	}, nil
}

func readFallback(toolName string) Action {
	return Action{ToolName: toolName, Operation: OperationRead, Risk: RiskLow, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}
}

func resolveWrite(req ResolveRequest) (Action, error) {
	var input writeInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return mutationFallback(req.ToolName, OperationWrite), fmt.Errorf("%w: parse write input", ErrInvalidInput)
	}
	if input.Path == "" {
		return mutationFallback(req.ToolName, OperationWrite), fmt.Errorf("%w: path is required", ErrInvalidInput)
	}
	target, err := toolpath.ResolveMutationTarget(input.Path, req.Context)
	if err != nil {
		return mutationFallback(req.ToolName, OperationWrite), fmt.Errorf("%w: resolve mutation target", ErrInvalidTarget)
	}
	kind := MutationKindCreate
	readRequirement := ReadRequirementNotRequired
	readState := ReadStateNotRequired
	if target.Exists {
		kind = MutationKindOverwrite
		readRequirement = ReadRequirementRequired
		readState = readStateFor(target.Path, req.Context)
	}
	return mutationAction(req.ToolName, OperationWrite, target, map[string]any{
		MetadataMutationKind:    kind,
		MetadataReadRequirement: readRequirement,
		MetadataReadState:       readState,
		MetadataTargetExists:    target.Exists,
	}), nil
}

func resolveEdit(req ResolveRequest) (Action, error) {
	var input editInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return mutationFallback(req.ToolName, OperationEdit), fmt.Errorf("%w: parse edit input", ErrInvalidInput)
	}
	if input.Path == "" {
		return mutationFallback(req.ToolName, OperationEdit), fmt.Errorf("%w: path is required", ErrInvalidInput)
	}
	target, err := toolpath.ResolveMutationTarget(input.Path, req.Context)
	if err != nil {
		return mutationFallback(req.ToolName, OperationEdit), fmt.Errorf("%w: resolve mutation target", ErrInvalidTarget)
	}
	readState := ReadStateMissing
	if target.Exists {
		readState = readStateFor(target.Path, req.Context)
	}
	return mutationAction(req.ToolName, OperationEdit, target, map[string]any{
		MetadataMutationKind:    MutationKindReplace,
		MetadataReadRequirement: ReadRequirementRequired,
		MetadataReadState:       readState,
		MetadataTargetExists:    target.Exists,
	}), nil
}

func resolveBash(req ResolveRequest) (Action, error) {
	var input bashInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return commandFallback(req.ToolName), fmt.Errorf("%w: parse bash input", ErrInvalidInput)
	}
	if strings.TrimSpace(input.Command) == "" {
		return commandFallback(req.ToolName), fmt.Errorf("%w: command is required", ErrInvalidInput)
	}
	classification := classifyCommand(input.Command)
	scope := ResourceScopeWorkspace
	if classification.Category == CommandCategoryUnknown {
		scope = ResourceScopeUnknown
	}
	return Action{
		ToolName:  req.ToolName,
		Operation: OperationExecute,
		Risk:      classification.Risk,
		Resources: []Resource{{Type: ResourceCommand, Scope: scope}},
		Metadata: map[string]any{
			MetadataCommandCategory:     string(classification.Category),
			MetadataCommandCapabilities: commandCapabilitiesStrings(classification.Capabilities),
			MetadataCommandRiskReasons:  commandRiskReasonStrings(classification.Reasons),
			MetadataCommandSummary:      classification.Summary,
		},
	}, nil
}

func commandFallback(toolName string) Action {
	return Action{ToolName: toolName, Operation: OperationExecute, Risk: RiskCritical, Resources: []Resource{{Type: ResourceCommand, Scope: ResourceScopeUnknown}}}
}

type webFetchInput struct {
	URL string `json:"url"`
}

func resolveWebFetch(req ResolveRequest) (Action, error) {
	var input webFetchInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return networkFallback(req.ToolName), fmt.Errorf("%w: parse webfetch input", ErrInvalidInput)
	}
	if strings.TrimSpace(input.URL) == "" {
		return networkFallback(req.ToolName), fmt.Errorf("%w: url is required", ErrInvalidInput)
	}
	action := networkFallback(req.ToolName)
	if host := networkHost(input.URL); host != "" {
		action.Metadata = map[string]any{MetadataNetworkHost: host}
	}
	return action, nil
}

func networkHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func networkFallback(toolName string) Action {
	return Action{
		ToolName:  toolName,
		Operation: OperationNetwork,
		Risk:      RiskMedium,
		Resources: []Resource{{Type: ResourceNetwork, Scope: ResourceScopeOutside}},
	}
}

func mutationFallback(toolName string, operation Operation) Action {
	return Action{ToolName: toolName, Operation: operation, Risk: RiskHigh, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}
}

func mutationAction(toolName string, operation Operation, target toolpath.MutationTarget, metadata map[string]any) Action {
	scope := ResourceScopeOutside
	if target.Within {
		scope = ResourceScopeWorkspace
	}
	return Action{
		ToolName:  toolName,
		Operation: operation,
		Risk:      RiskHigh,
		Resources: []Resource{{Type: ResourceFile, Scope: scope, Path: target.Path}},
		Metadata:  metadata,
	}
}

func readStateFor(path string, tctx *tool.Context) string {
	switch toolpath.FreshReadState(path, tctx) {
	case toolpath.ReadStateFresh:
		return ReadStateFresh
	case toolpath.ReadStatePartial:
		return ReadStatePartial
	case toolpath.ReadStateStale:
		return ReadStateStale
	default:
		return ReadStateMissing
	}
}

func resourceScope(workspace string, path string) ResourceScope {
	within, err := toolpath.IsWithinResolved(workspace, path)
	if err != nil {
		return ResourceScopeUnknown
	}
	if within {
		return ResourceScopeWorkspace
	}
	return ResourceScopeOutside
}
