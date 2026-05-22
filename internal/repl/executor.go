package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/wt68/runcode/pkg/tool"
)

var (
	ErrInvalidToolRequest = errors.New("invalid tool request")
	ErrUnknownTool        = errors.New("unknown tool")
)

type Executor struct {
	tools map[string]tool.Tool
}

type ExecuteRequest struct {
	Name      string
	Input     json.RawMessage
	ToolUseID string
	Context   *tool.Context
	Events    chan<- tool.Event
}

type ExecuteResult struct {
	ToolName  string
	ToolUseID string
	Result    tool.Result
}

func NewExecutor(toolList []tool.Tool) (*Executor, error) {
	indexed := make(map[string]tool.Tool, len(toolList))
	for _, candidate := range toolList {
		if isNilTool(candidate) {
			return nil, fmt.Errorf("%w: nil tool", ErrInvalidToolRequest)
		}
		name := candidate.Name()
		if name == "" {
			return nil, fmt.Errorf("%w: tool name is required", ErrInvalidToolRequest)
		}
		if _, exists := indexed[name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %q", ErrInvalidToolRequest, name)
		}
		indexed[name] = candidate
	}

	return &Executor{tools: indexed}, nil
}

func isNilTool(candidate tool.Tool) bool {
	if candidate == nil {
		return true
	}
	value := reflect.ValueOf(candidate)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (e *Executor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if req.Name == "" {
		return ExecuteResult{}, fmt.Errorf("%w: tool name is required", ErrInvalidToolRequest)
	}

	runner, ok := e.tools[req.Name]
	if !ok {
		return ExecuteResult{}, fmt.Errorf("%w: %s", ErrUnknownTool, req.Name)
	}

	tctx := req.Context
	if tctx == nil {
		tctx = &tool.Context{}
	}
	if req.ToolUseID != "" {
		tctx.ToolUseID = req.ToolUseID
	}

	result, err := runner.Run(ctx, req.Input, tctx, req.Events)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("run tool %q: %w", req.Name, err)
	}

	return ExecuteResult{ToolName: req.Name, ToolUseID: tctx.ToolUseID, Result: result}, nil
}
