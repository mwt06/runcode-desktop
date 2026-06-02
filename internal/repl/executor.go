package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/tool"
)

var (
	ErrInvalidToolRequest = errors.New("invalid tool request")
	ErrUnknownTool        = errors.New("unknown tool")
)

type Executor struct {
	tools       map[string]tool.Tool
	permissions *permissions.Service
}

type ExecutorOptions struct {
	Tools       []tool.Tool
	Permissions *permissions.Service
}

type ExecuteRequest struct {
	Name      string
	Input     json.RawMessage
	ToolUseID string
	Context   *tool.Context
	Events    chan<- tool.Event
	Telemetry telemetry.Recorder
	TraceID   string
	TurnID    string
}

type ExecuteResult struct {
	ToolName  string
	ToolUseID string
	Result    tool.Result
}

func NewExecutor(toolList []tool.Tool) (*Executor, error) {
	return NewExecutorWithOptions(ExecutorOptions{Tools: toolList})
}

func NewExecutorWithOptions(opts ExecutorOptions) (*Executor, error) {
	indexed := make(map[string]tool.Tool, len(opts.Tools))
	for _, candidate := range opts.Tools {
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

	permissionService := opts.Permissions
	if permissionService == nil {
		permissionService = permissions.DefaultService()
	}
	return &Executor{tools: indexed, permissions: permissionService}, nil
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

// IsConcurrencySafe reports whether the named tool can run concurrently with sibling tool calls.
func (e *Executor) IsConcurrencySafe(name string) bool {
	t, ok := e.tools[name]
	return ok && t.IsConcurrencySafe()
}

// ApprovalAvailable reports whether the executor's permission service requires interactive approval.
func (e *Executor) ApprovalAvailable() bool {
	return e.permissions != nil && e.permissions.ApprovalAvailable()
}

func (e *Executor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if req.Name == "" {
		return ExecuteResult{}, fmt.Errorf("%w: tool name is required", ErrInvalidToolRequest)
	}
	recorder := req.Telemetry
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	started := time.Now()

	runner, ok := e.tools[req.Name]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrUnknownTool, req.Name)
		recordToolError(ctx, recorder, req, started, err)
		return unknownToolResult(req), nil
	}

	tctx := req.Context
	if tctx == nil {
		tctx = &tool.Context{}
	}
	if req.ToolUseID != "" {
		tctx.ToolUseID = req.ToolUseID
	}

	action, decision := e.permissions.AuthorizeTool(ctx, permissions.ResolveRequest{ToolName: req.Name, Input: req.Input, Context: tctx})
	permissions.RecordDecision(ctx, recorder, permissions.TelemetryRequest{
		TraceID:           req.TraceID,
		TurnID:            req.TurnID,
		ToolUseID:         tctx.ToolUseID,
		Mode:              e.permissions.Mode(),
		ApprovalAvailable: e.permissions.ApprovalAvailable(),
		Action:            action,
		Decision:          decision,
	})
	if decision.FinalEffect != permissions.EffectAllow {
		return permissionDeniedResult(req.Name, tctx.ToolUseID, decision), nil
	}

	recorder.Record(ctx, telemetry.Event{
		Time:      started.UTC(),
		Name:      telemetry.EventToolStart,
		TraceID:   req.TraceID,
		TurnID:    req.TurnID,
		ToolUseID: tctx.ToolUseID,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrToolName):   req.Name,
			string(telemetry.AttrInputBytes): len(req.Input),
			string(telemetry.AttrHasContext): req.Context != nil,
		},
	})

	result, err := runner.Run(ctx, req.Input, tctx, req.Events)
	if err != nil {
		if isUnrecoverableToolError(err) {
			return ExecuteResult{}, err
		}
		recordToolError(ctx, recorder, req, started, fmt.Errorf("run tool %q: %w", req.Name, err))
		return toolRunErrorResult(req, tctx, err), nil
	}

	recorder.Record(ctx, telemetry.Event{
		Time:      time.Now().UTC(),
		Name:      telemetry.EventToolEnd,
		TraceID:   req.TraceID,
		TurnID:    req.TurnID,
		ToolUseID: tctx.ToolUseID,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrToolName):          req.Name,
			string(telemetry.AttrInputBytes):        len(req.Input),
			string(telemetry.AttrHasContext):        req.Context != nil,
			string(telemetry.AttrContentBlockCount): len(result.Content),
			string(telemetry.AttrIsErrorResult):     result.IsError,
			string(telemetry.AttrDurationMS):        telemetry.DurationMS(time.Since(started)),
		},
	})

	return ExecuteResult{ToolName: req.Name, ToolUseID: tctx.ToolUseID, Result: result}, nil
}

func unknownToolResult(req ExecuteRequest) ExecuteResult {
	return ExecuteResult{
		ToolName:  req.Name,
		ToolUseID: req.ToolUseID,
		Result: tool.Result{
			IsError: true,
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("Tool error: unknown tool %q.", req.Name)}},
		},
	}
}

func toolRunErrorResult(req ExecuteRequest, tctx *tool.Context, err error) ExecuteResult {
	return ExecuteResult{
		ToolName:  req.Name,
		ToolUseID: tctx.ToolUseID,
		Result: tool.Result{
			IsError: true,
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("Tool error in %s: %v", req.Name, err)}},
		},
	}
}

func isUnrecoverableToolError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func permissionDeniedResult(toolName string, toolUseID string, decision permissions.Decision) ExecuteResult {
	return ExecuteResult{
		ToolName:  toolName,
		ToolUseID: toolUseID,
		Result: tool.Result{
			IsError: true,
			Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: fmt.Sprintf("Permission denied: this tool action is not allowed by the current policy. reason=%s final_effect=%s", decision.Reason, decision.FinalEffect)}},
		},
	}
}

func recordToolError(ctx context.Context, recorder telemetry.Recorder, req ExecuteRequest, started time.Time, err error) {
	recorder.Record(ctx, telemetry.Event{
		Time:      time.Now().UTC(),
		Name:      telemetry.EventToolError,
		TraceID:   req.TraceID,
		TurnID:    req.TurnID,
		ToolUseID: req.ToolUseID,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrToolName):   req.Name,
			string(telemetry.AttrInputBytes): len(req.Input),
			string(telemetry.AttrError):      "tool_execution_failed",
			string(telemetry.AttrDurationMS): telemetry.DurationMS(time.Since(started)),
		},
	})
}
