package permissions

import (
	"context"
	"encoding/json"
	"fmt"

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

func (DefaultResolver) Resolve(_ context.Context, req ResolveRequest) (Action, error) {
	switch req.ToolName {
	case "Read":
		return resolveRead(req)
	case "Write":
		return Action{ToolName: req.ToolName, Operation: OperationWrite, Risk: RiskHigh, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, nil
	case "Edit":
		return Action{ToolName: req.ToolName, Operation: OperationEdit, Risk: RiskHigh, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, nil
	case "Bash":
		return Action{ToolName: req.ToolName, Operation: OperationExecute, Risk: RiskCritical, Resources: []Resource{{Type: ResourceCommand, Scope: ResourceScopeUnknown}}}, nil
	default:
		return Action{ToolName: req.ToolName, Operation: OperationUnknown, Risk: RiskHigh, Resources: []Resource{{Type: ResourceUnknown, Scope: ResourceScopeUnknown}}}, nil
	}
}

func resolveRead(req ResolveRequest) (Action, error) {
	var input readInput
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return Action{ToolName: req.ToolName, Operation: OperationRead, Risk: RiskLow, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, fmt.Errorf("%w: parse read input", ErrInvalidInput)
	}
	if input.Path == "" {
		return Action{ToolName: req.ToolName, Operation: OperationRead, Risk: RiskLow, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, fmt.Errorf("%w: path is required", ErrInvalidInput)
	}
	workspace, err := toolpath.WorkspaceRoot(req.Context)
	if err != nil {
		return Action{ToolName: req.ToolName, Operation: OperationRead, Risk: RiskLow, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, fmt.Errorf("%w: resolve workspace", ErrInvalidInput)
	}
	path, err := toolpath.Resolve(input.Path, req.Context)
	if err != nil {
		return Action{ToolName: req.ToolName, Operation: OperationRead, Risk: RiskLow, Resources: []Resource{{Type: ResourceFile, Scope: ResourceScopeUnknown}}}, fmt.Errorf("%w: resolve path", ErrInvalidInput)
	}
	return Action{
		ToolName:  req.ToolName,
		Operation: OperationRead,
		Risk:      RiskLow,
		Resources: []Resource{{Type: ResourceFile, Scope: resourceScope(workspace, path), Path: path}},
	}, nil
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
