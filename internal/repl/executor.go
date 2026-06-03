package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

var (
	ErrInvalidToolRequest = errors.New("invalid tool request")
	ErrUnknownTool        = errors.New("unknown tool")
)

const (
	toolEventForwarderBufferSize = 32
	maxToolEventFiles            = 50
	maxToolEventOutputLines      = 20
	maxToolEventOutputLineRunes  = 200
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
		emitToolEvent(req.Events, executorToolEvent(tool.EventTypeFailed, req.Name, req.ToolUseID, "unknown tool"))
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
		emitToolEvent(req.Events, executorToolEvent(tool.EventTypeFailed, req.Name, tctx.ToolUseID, "permission denied"))
		return permissionDeniedResult(req.Name, tctx.ToolUseID, decision), nil
	}
	emitToolEvent(req.Events, executorToolEvent(tool.EventTypeStarted, req.Name, tctx.ToolUseID, "started"))

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

	readSetBefore := cloneReadSetForEvents(tctx.ReadSet)
	toolEvents, finishToolEvents := toolEventForwarder(req.Events, req.Name, tctx.ToolUseID)
	result, err := runner.Run(ctx, req.Input, tctx, toolEvents)
	finishToolEvents()
	readFiles, readFilesTotal := readSetDeltaFileReferences(readSetBefore, tctx.ReadSet, tctx)
	outputLines, outputTotal, outputTruncated := toolOutputForEvents(req.Name, result)
	if err != nil {
		if isUnrecoverableToolError(err) {
			event := executorToolEvent(tool.EventTypeFailed, req.Name, tctx.ToolUseID, "cancelled")
			event.Files = readFiles
			event.FilesTotal = readFilesTotal
			emitToolEvent(req.Events, event)
			return ExecuteResult{}, err
		}
		recordToolError(ctx, recorder, req, started, fmt.Errorf("run tool %q: %w", req.Name, err))
		event := executorToolEvent(tool.EventTypeFailed, req.Name, tctx.ToolUseID, "failed")
		event.Files = readFiles
		event.FilesTotal = readFilesTotal
		attachToolOutput(&event, outputLines, outputTotal, outputTruncated)
		emitToolEvent(req.Events, event)
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

	if result.IsError {
		event := executorToolEvent(tool.EventTypeFailed, req.Name, tctx.ToolUseID, "completed with error")
		event.Files = readFiles
		event.FilesTotal = readFilesTotal
		attachToolOutput(&event, outputLines, outputTotal, outputTruncated)
		emitToolEvent(req.Events, event)
	} else {
		event := executorToolEvent(tool.EventTypeCompleted, req.Name, tctx.ToolUseID, "completed")
		event.Files = readFiles
		event.FilesTotal = readFilesTotal
		attachToolOutput(&event, outputLines, outputTotal, outputTruncated)
		emitToolEvent(req.Events, event)
	}
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

func executorToolEvent(eventType tool.EventType, toolName string, toolUseID string, message string) tool.Event {
	return normalizeToolEvent(tool.Event{Type: eventType, ToolName: toolName, ToolUseID: toolUseID, Message: message}, toolName, toolUseID)
}

func emitToolEvent(out chan<- tool.Event, event tool.Event) {
	if out == nil {
		return
	}
	select {
	case out <- event:
	default:
	}
}

func toolEventForwarder(out chan<- tool.Event, toolName string, toolUseID string) (chan<- tool.Event, func()) {
	in := make(chan tool.Event, toolEventForwarderBufferSize)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range in {
			emitToolEvent(out, normalizeToolEvent(event, toolName, toolUseID))
		}
	}()
	return in, func() {
		close(in)
		<-done
	}
}

func normalizeToolEvent(event tool.Event, toolName string, toolUseID string) tool.Event {
	if event.ToolName == "" {
		event.ToolName = toolName
	}
	if event.ToolUseID == "" {
		event.ToolUseID = toolUseID
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	return event
}

func cloneReadSetForEvents(readSet map[string]tool.ReadFile) map[string]tool.ReadFile {
	if len(readSet) == 0 {
		return nil
	}
	cloned := make(map[string]tool.ReadFile, len(readSet))
	for path, file := range readSet {
		cloned[path] = file
	}
	return cloned
}

func readSetDeltaFileReferences(before map[string]tool.ReadFile, after map[string]tool.ReadFile, tctx *tool.Context) ([]tool.FileReference, int) {
	if len(after) == 0 {
		return nil, 0
	}
	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return nil, 0
	}
	seen := map[string]struct{}{}
	refs := make([]tool.FileReference, 0)
	for key, file := range after {
		if previous, ok := before[key]; ok && sameReadFile(previous, file) {
			continue
		}
		filePath := file.Path
		if filePath == "" {
			filePath = key
		}
		rel, ok := safeWorkspaceRelativeEventPath(workspace, filePath)
		if !ok {
			continue
		}
		if _, exists := seen[rel]; exists {
			continue
		}
		seen[rel] = struct{}{}
		refs = append(refs, tool.FileReference{Path: rel, Kind: tool.FileReferenceRead})
	}
	sort.Slice(refs, func(i int, j int) bool { return refs[i].Path < refs[j].Path })
	total := len(refs)
	if len(refs) > maxToolEventFiles {
		refs = refs[:maxToolEventFiles]
	}
	return refs, total
}

func attachToolOutput(event *tool.Event, lines []tool.OutputLine, total int, truncated bool) {
	event.Output = lines
	event.OutputTotal = total
	event.OutputTruncated = truncated
}

// toolOutputForEvents builds a bounded, sanitized output excerpt for UI display.
// It prefers a tool-supplied structured Result.Output (e.g. a diff) and otherwise
// derives a generic excerpt from the result content. Glob is suppressed because its
// matched files are already surfaced as file references. The returned excerpt is
// display-only and never recorded to telemetry or transcripts.
func toolOutputForEvents(name string, result tool.Result) ([]tool.OutputLine, int, bool) {
	if name == "Glob" {
		return nil, 0, false
	}
	var lines []tool.OutputLine
	if len(result.Output) > 0 {
		lines = sanitizeOutputLines(result.Output)
	} else {
		lines = sanitizeOutputLines(genericToolOutput(name, result))
	}
	total := len(lines)
	truncated := false
	if total > maxToolEventOutputLines {
		lines = lines[:maxToolEventOutputLines]
		truncated = true
	}
	if len(lines) == 0 {
		return nil, 0, false
	}
	return lines, total, truncated
}

func genericToolOutput(name string, result tool.Result) []tool.OutputLine {
	text := strings.TrimRight(resultText(result), "\n")
	if strings.TrimSpace(text) == "" {
		return nil
	}
	stream := tool.OutputStreamStdout
	switch {
	case result.IsError:
		stream = tool.OutputStreamStderr
	case name == "Grep":
		stream = tool.OutputStreamMatch
	}
	rawLines := strings.Split(text, "\n")
	lines := make([]tool.OutputLine, 0, len(rawLines))
	for _, raw := range rawLines {
		lines = append(lines, tool.OutputLine{Stream: stream, Text: raw})
	}
	return lines
}

func resultText(result tool.Result) string {
	var b strings.Builder
	for _, content := range result.Content {
		if content.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(content.Text)
	}
	return b.String()
}

func sanitizeOutputLines(lines []tool.OutputLine) []tool.OutputLine {
	out := make([]tool.OutputLine, 0, len(lines))
	for _, line := range lines {
		stream := line.Stream
		if stream == "" {
			stream = tool.OutputStreamStdout
		}
		out = append(out, tool.OutputLine{Stream: stream, Text: sanitizeOutputText(line.Text)})
	}
	return out
}

func sanitizeOutputText(text string) string {
	text = strings.ReplaceAll(text, "\t", "    ")
	var b strings.Builder
	for _, r := range text {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return truncateOutputRunes(b.String(), maxToolEventOutputLineRunes)
}

func truncateOutputRunes(value string, width int) string {
	if width <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

func sameReadFile(a tool.ReadFile, b tool.ReadFile) bool {
	return a.Path == b.Path && a.Size == b.Size && a.Complete == b.Complete && a.ModTime.Equal(b.ModTime)
}

func safeWorkspaceRelativeEventPath(workspace string, filePath string) (string, bool) {
	if filePath == "" {
		return "", false
	}
	abs, err := filepath.Abs(filepath.Clean(filePath))
	if err != nil {
		return "", false
	}
	within, err := toolpath.IsWithinResolved(workspace, abs)
	if err != nil || !within {
		return "", false
	}
	rel, err := filepath.Rel(workspace, abs)
	if err != nil || rel == "." || filepath.IsAbs(rel) {
		return "", false
	}
	slashRel := filepath.ToSlash(rel)
	if slashRel == "" || slashRel == "." || slashRel == ".." || strings.HasPrefix(slashRel, "../") {
		return "", false
	}
	return slashRel, true
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
