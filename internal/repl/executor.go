package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/wt68/runcode/internal/telemetry"
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
	recorder := req.Telemetry
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	started := time.Now()

	runner, ok := e.tools[req.Name]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrUnknownTool, req.Name)
		recordToolError(ctx, recorder, req, started, err)
		return ExecuteResult{}, err
	}

	tctx := req.Context
	if tctx == nil {
		tctx = &tool.Context{}
	}
	if req.ToolUseID != "" {
		tctx.ToolUseID = req.ToolUseID
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
		err = fmt.Errorf("run tool %q: %w", req.Name, err)
		recordToolError(ctx, recorder, req, started, err)
		return ExecuteResult{}, err
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
			string(telemetry.AttrDurationMS):        telemetry.DurationMS(time.Since(started)),
		},
	})

	return ExecuteResult{ToolName: req.Name, ToolUseID: tctx.ToolUseID, Result: result}, nil
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
			string(telemetry.AttrError):      err.Error(),
			string(telemetry.AttrDurationMS): telemetry.DurationMS(time.Since(started)),
		},
	})
}
