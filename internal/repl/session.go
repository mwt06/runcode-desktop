package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"time"

	"golang.org/x/sync/errgroup"

	"github.com/wt68/runcode/internal/compaction"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/prompt/sections"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
)

const DefaultMaxIterations = 8

var (
	ErrInvalidSession = errors.New("invalid session")
	ErrMaxIterations  = errors.New("max react iterations reached")
)

type SessionOptions struct {
	Provider      llm.Provider
	Model         string
	Tools         []tool.Tool
	Prompt        prompt.AssemblerOpts
	MaxTokens     int
	Temperature   *float64
	Metadata      map[string]any
	ToolContext   *tool.Context
	ToolEvents    chan<- tool.Event
	MaxIterations int
	Reasoning     ReasoningOptions
	Telemetry     telemetry.Recorder
	TraceID       string
	Permissions   *permissions.Service
	Transcript    transcript.Recorder
	SessionID     string
	// MaxHistoryMessages bounds the number of messages retained across turns.
	// 0 (default) disables trimming.
	MaxHistoryMessages int
	// StreamDelta is called with each text delta as it arrives from the provider.
	// Only text blocks trigger the callback; tool_use and thinking deltas are skipped.
	// nil disables streaming.
	StreamDelta func(delta string)
	// InitialHistory seeds the session's working history, e.g. when resuming a
	// persisted session. nil starts a fresh conversation.
	InitialHistory []llm.Message
	// SessionStore persists the full conversation for cross-process resume.
	// nil disables persistence.
	SessionStore sessions.Store
	// MaxContextTokens enables context compaction once a turn's input tokens
	// approach this budget. 0 (default) disables compaction.
	MaxContextTokens int
}

type Session struct {
	provider           llm.Provider
	model              string
	tools              []tool.Tool
	executor           *Executor
	prompt             prompt.AssemblerOpts
	maxTokens          int
	temperature        *float64
	metadata           map[string]any
	toolContext        *tool.Context
	toolEvents         chan<- tool.Event
	maxIterations      int
	reasoning          ReasoningOptions
	telemetry          telemetry.Recorder
	traceID            string
	transcript         transcript.Recorder
	sessionID          string
	history            []llm.Message
	maxHistoryMessages int
	streamDelta        func(delta string)
	sessionStore       sessions.Store
	maxContextTokens   int
}

type TurnResult struct {
	FirstRequest            llm.Request
	FinalAssistant          llm.Message
	LastToolMessage         *llm.Message
	ToolResults             []llm.ContentBlock
	FinalStopReason         llm.StopReason
	FinalUsage              *llm.Usage
	Requests                []llm.Request
	AssistantMessages       []llm.Message
	ToolMessages            []llm.Message
	Usages                  []*llm.Usage
	Iterations              int
	ReasoningClassification *ReasoningClassification
	ClassificationRequest   *llm.Request
	ClassificationUsage     *llm.Usage
}

func NewSession(opts SessionOptions) (*Session, error) {
	if opts.Provider == nil {
		return nil, fmt.Errorf("%w: provider is required", ErrInvalidSession)
	}
	maxIterations, err := normalizeMaxIterations(opts.MaxIterations)
	if err != nil {
		return nil, err
	}
	maxHistoryMessages, err := normalizeMaxHistoryMessages(opts.MaxHistoryMessages)
	if err != nil {
		return nil, err
	}

	tools := cloneTools(opts.Tools)
	executor, err := NewExecutorWithOptions(ExecutorOptions{Tools: tools, Permissions: opts.Permissions})
	if err != nil {
		return nil, err
	}
	recorder := opts.Telemetry
	if recorder == nil {
		recorder = telemetry.Noop()
	}
	traceID := opts.TraceID
	if traceID == "" {
		traceID = telemetry.NewTraceID()
	}
	transcriptRecorder := opts.Transcript
	if transcriptRecorder == nil {
		transcriptRecorder = transcript.Noop()
	}
	sessionStore := opts.SessionStore
	if sessionStore == nil {
		sessionStore = sessions.Noop()
	}

	return &Session{
		provider:           opts.Provider,
		model:              opts.Model,
		tools:              tools,
		executor:           executor,
		prompt:             opts.Prompt,
		maxTokens:          opts.MaxTokens,
		temperature:        opts.Temperature,
		metadata:           opts.Metadata,
		toolContext:        opts.ToolContext,
		toolEvents:         opts.ToolEvents,
		maxIterations:      maxIterations,
		reasoning:          opts.Reasoning,
		telemetry:          recorder,
		traceID:            traceID,
		transcript:         transcriptRecorder,
		sessionID:          opts.SessionID,
		history:            cloneMessages(opts.InitialHistory),
		maxHistoryMessages: maxHistoryMessages,
		streamDelta:        opts.StreamDelta,
		sessionStore:       sessionStore,
		maxContextTokens:   opts.MaxContextTokens,
	}, nil
}

func (s *Session) RunTurn(ctx context.Context, userText string) (TurnResult, error) {
	messages := cloneMessages(s.history)
	messages = append(messages, userMessage(userText))
	currentUserIndex := len(messages) - 1
	promptOpts := s.prompt
	var result TurnResult
	turn := s.startTurn(ctx, userText)
	defer func() {
		if recovered := recover(); recovered != nil {
			turn.error(ctx, result, fmt.Errorf("panic: %v", recovered))
			panic(recovered)
		}
	}()

	if s.reasoning.Enabled {
		classification, req, usage, err := s.classifyReasoningScenario(ctx, userText, turn.id)
		if err != nil {
			if s.reasoning.Strict {
				return result, turn.error(ctx, result, err)
			}
			classification = ReasoningClassification{Scenario: defaultReasoningScenario(s.reasoning)}
		}
		result.ReasoningClassification = &classification
		result.ClassificationRequest = req
		result.ClassificationUsage = usage
		promptOpts.Reasoning = sections.ReasoningGuidance(string(classification.Scenario))
	}

	for iteration := 0; iteration < s.maxIterations; iteration++ {
		messages, currentUserIndex = trimMessagesForHistoryBudget(messages, s.maxHistoryMessages, currentUserIndex)
		req, err := s.buildRequestWithMessagesAndPrompt(messages, promptOpts)
		if err != nil {
			return result, turn.error(ctx, result, err)
		}

		assistant, stopReason, usage, err := s.streamAssistant(ctx, req, turn.id, "assistant")
		if err != nil {
			return result, turn.error(ctx, result, err)
		}

		if iteration == 0 {
			result.FirstRequest = req
		}
		result.Requests = append(result.Requests, req)
		result.FinalAssistant = assistant
		result.AssistantMessages = append(result.AssistantMessages, assistant)
		result.FinalStopReason = stopReason
		result.FinalUsage = usage
		result.Usages = append(result.Usages, usage)
		result.Iterations = iteration + 1

		messages = append(messages, assistant)
		if !hasToolUse(assistant) {
			nextHistory, _ := trimMessagesForHistoryBudget(messages, s.maxHistoryMessages, currentUserIndex)
			if err := s.recordTranscriptTurn(ctx, turn.id, userText, result); err != nil {
				return result, turn.error(ctx, result, err)
			}
			// Persist this turn's complete new messages (the mandatory segment is
			// never truncated by trimming) before committing the in-memory working
			// set, which may be trimmed/compacted.
			turnMessages := cloneMessages(messages[currentUserIndex:])
			s.history = nextHistory
			s.persistTurn(ctx, turn.id, turnMessages)
			s.history = s.maybeCompact(ctx, turn.id, s.history, result.FinalUsage)
			return result, turn.end(ctx, result)
		}
		if iteration == s.maxIterations-1 {
			return result, turn.error(ctx, result, ErrMaxIterations)
		}

		toolResults, err := s.executeToolUses(ctx, assistant, turn.id)
		if err != nil {
			return result, turn.error(ctx, result, err)
		}
		toolMessage := llm.Message{Role: llm.RoleTool, Content: toolResults}
		result.LastToolMessage = &toolMessage
		result.ToolMessages = append(result.ToolMessages, toolMessage)
		result.ToolResults = append(result.ToolResults, toolResults...)
		messages = append(messages, toolMessage)
	}

	return result, turn.error(ctx, result, ErrMaxIterations)
}

func (s *Session) History() []llm.Message {
	return cloneMessages(s.history)
}

func (s *Session) ResetHistory() {
	s.history = nil
}

const (
	compactionThresholdRatio   = 0.8
	compactionSummaryMaxTokens = 2048
)

const compactionSystemPrompt = "You are condensing a coding-assistant conversation to save context. " +
	"Write a concise summary that preserves key decisions, file paths, code changes, the current task " +
	"state, and any unresolved follow-ups. Omit pleasantries and verbatim tool output."

const compactionInstruction = "Summarize the conversation so far per the system instructions."

// persistTurn appends the turn's complete new messages to the session store.
// Persistence failures are recorded and swallowed so they never interrupt the
// conversation; the on-disk history simply misses a turn.
func (s *Session) persistTurn(ctx context.Context, turnID string, messages []llm.Message) {
	if s.sessionStore == nil || len(messages) == 0 {
		return
	}
	if err := s.sessionStore.Append(ctx, messages); err != nil {
		s.record(ctx, telemetry.Event{
			Time:       time.Now().UTC(),
			Name:       telemetry.EventSessionPersistErr,
			TraceID:    s.traceID,
			TurnID:     turnID,
			Attributes: telemetry.Attrs{string(telemetry.AttrError): "session_persist_failed"},
		})
	}
}

// maybeCompact compacts the working history when the last turn's input tokens
// approach the context budget. It uses the provider-reported input tokens as a
// free token estimate and never errors out the turn — a failed compaction leaves
// history untouched.
func (s *Session) maybeCompact(ctx context.Context, turnID string, history []llm.Message, usage *llm.Usage) []llm.Message {
	if s.maxContextTokens <= 0 || usage == nil {
		return history
	}
	if usage.InputTokens <= int(float64(s.maxContextTokens)*compactionThresholdRatio) {
		return history
	}
	compacted, err := compaction.Compact(ctx, history, compaction.Options{Summarize: s.summarizeForCompaction(turnID)})
	if err != nil {
		s.record(ctx, telemetry.Event{
			Time:       time.Now().UTC(),
			Name:       telemetry.EventCompactionErr,
			TraceID:    s.traceID,
			TurnID:     turnID,
			Attributes: telemetry.Attrs{string(telemetry.AttrError): "compaction_failed"},
		})
		return history
	}
	return compacted
}

func (s *Session) summarizeForCompaction(turnID string) compaction.Summarizer {
	return func(ctx context.Context, messages []llm.Message) (string, error) {
		conversation := append(cloneMessages(messages), userMessage(compactionInstruction))
		req := llm.Request{
			Model:     s.model,
			Messages:  conversation,
			System:    []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: compactionSystemPrompt}},
			MaxTokens: compactionSummaryMaxTokens,
			Metadata:  s.metadata,
		}
		assistant, _, _, err := s.streamAssistant(ctx, req, turnID, "compaction_summary")
		if err != nil {
			return "", err
		}
		return llm.TextContent(assistant), nil
	}
}

func (s *Session) recordTranscriptTurn(ctx context.Context, turnID string, userText string, result TurnResult) error {
	return s.transcript.RecordTurn(ctx, transcript.BuildTurnRecord(transcript.TurnInput{
		SessionID:         s.sessionID,
		TraceID:           s.traceID,
		TurnID:            turnID,
		CWD:               workingDirectory(s.toolContext),
		Model:             s.model,
		UserText:          userText,
		FinalAssistant:    result.FinalAssistant,
		AssistantMessages: result.AssistantMessages,
		ToolResults:       result.ToolResults,
		StopReason:        result.FinalStopReason,
		Iterations:        result.Iterations,
		Usage:             result.FinalUsage,
	}))
}

func workingDirectory(tctx *tool.Context) string {
	if tctx == nil {
		return ""
	}
	return tctx.WorkingDirectory
}

func (s *Session) buildRequest(userText string) (llm.Request, error) {
	return s.buildRequestWithMessages([]llm.Message{userMessage(userText)})
}

func (s *Session) buildRequestWithMessages(messages []llm.Message) (llm.Request, error) {
	return s.buildRequestWithMessagesAndPrompt(messages, s.prompt)
}

func (s *Session) buildRequestWithMessagesAndPrompt(messages []llm.Message, promptOpts prompt.AssemblerOpts) (llm.Request, error) {
	promptOpts.Tools = s.tools
	system, err := prompt.BuildSystemPrompt(promptOpts)
	if err != nil {
		return llm.Request{}, err
	}

	return llm.Request{
		Model:       s.model,
		Messages:    cloneMessages(messages),
		System:      system,
		Tools:       ToolSpecs(s.tools),
		MaxTokens:   s.maxTokens,
		Temperature: s.temperature,
		Metadata:    s.metadata,
	}, nil
}

func (s *Session) streamAssistant(ctx context.Context, req llm.Request, turnID string, purpose string) (llm.Message, llm.StopReason, *llm.Usage, error) {
	requestID := telemetry.NewRequestID()
	started := time.Now()
	s.record(ctx, telemetry.Event{
		Time:      started.UTC(),
		Name:      telemetry.EventLLMRequestStart,
		TraceID:   s.traceID,
		TurnID:    turnID,
		RequestID: requestID,
		Attributes: telemetry.Attrs{
			string(telemetry.AttrProvider):       s.provider.Name(),
			string(telemetry.AttrModel):          req.Model,
			string(telemetry.AttrPurpose):        purpose,
			string(telemetry.AttrMessageCount):   len(req.Messages),
			string(telemetry.AttrToolCount):      len(req.Tools),
			string(telemetry.AttrMaxTokens):      req.MaxTokens,
			string(telemetry.AttrHasTemperature): req.Temperature != nil,
		},
	})
	stream, err := s.provider.Stream(ctx, req)
	if err != nil {
		err = fmt.Errorf("stream provider: %w", err)
		s.record(ctx, s.llmErrorEvent(requestID, turnID, purpose, req, started, err))
		return llm.Message{}, "", nil, err
	}
	defer stream.Close()
	var deltaFn func(string)
	if purpose == "assistant" {
		deltaFn = s.streamDelta
	}
	message, stopReason, usage, err := collectAssistantMessage(ctx, stream, deltaFn)
	if err != nil {
		s.record(ctx, s.llmErrorEvent(requestID, turnID, purpose, req, started, err))
		return llm.Message{}, "", nil, err
	}
	s.record(ctx, telemetry.Event{
		Time:      time.Now().UTC(),
		Name:      telemetry.EventLLMRequestEnd,
		TraceID:   s.traceID,
		TurnID:    turnID,
		RequestID: requestID,
		Attributes: telemetry.MergeAttrs(telemetry.Attrs{
			string(telemetry.AttrProvider):     s.provider.Name(),
			string(telemetry.AttrModel):        req.Model,
			string(telemetry.AttrPurpose):      purpose,
			string(telemetry.AttrStopReason):   string(stopReason),
			string(telemetry.AttrDurationMS):   telemetry.DurationMS(time.Since(started)),
			string(telemetry.AttrMessageCount): len(req.Messages),
			string(telemetry.AttrToolCount):    len(req.Tools),
		}, telemetry.UsageAttrs(usage)),
	})
	return message, stopReason, usage, nil
}

func (s *Session) classifyReasoningScenario(ctx context.Context, userText string, turnID string) (ReasoningClassification, *llm.Request, *llm.Usage, error) {
	temperature := 0.0
	req := llm.Request{
		Model:    s.model,
		Messages: []llm.Message{userMessage(userText)},
		System: []llm.ContentBlock{{
			Type:  llm.ContentBlockTypeText,
			Text:  sections.ReasoningClassifier(),
			Cache: llm.CacheControlNone,
		}},
		MaxTokens:   reasoningMaxTokens(s.reasoning),
		Temperature: &temperature,
		Metadata:    s.metadata,
	}
	assistant, _, usage, err := s.streamAssistant(ctx, req, turnID, "reasoning_classification")
	if err != nil {
		return ReasoningClassification{}, &req, usage, err
	}
	classification, err := parseReasoningClassification(llm.TextContent(assistant))
	if err != nil {
		return ReasoningClassification{}, &req, usage, err
	}
	return classification, &req, usage, nil
}

func (s *Session) executeToolUses(ctx context.Context, assistant llm.Message, turnID string) ([]llm.ContentBlock, error) {
	var blocks []llm.ContentBlock
	for _, b := range assistant.Content {
		if b.Type == llm.ContentBlockTypeToolUse {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return nil, nil
	}
	results := make([]llm.ContentBlock, len(blocks))
	i := 0
	for i < len(blocks) {
		if !s.canRunConcurrently(blocks[i]) {
			result, err := s.executeSingleTool(ctx, blocks[i], turnID)
			if err != nil {
				return nil, err
			}
			results[i] = result
			i++
			continue
		}
		j := i + 1
		for j < len(blocks) && s.canRunConcurrently(blocks[j]) {
			j++
		}
		if err := s.executeConcurrentBatch(ctx, blocks[i:j], results[i:j], turnID); err != nil {
			return nil, err
		}
		i = j
	}
	return results, nil
}

func (s *Session) canRunConcurrently(block llm.ContentBlock) bool {
	return s.executor.IsConcurrencySafe(block.Name) && !s.executor.ApprovalAvailable()
}

func (s *Session) executeSingleTool(ctx context.Context, block llm.ContentBlock, turnID string) (llm.ContentBlock, error) {
	executed, err := s.executor.Execute(ctx, ExecuteRequest{
		Name:      block.Name,
		Input:     block.Input,
		ToolUseID: block.ID,
		Context:   s.toolContext,
		Events:    s.toolEvents,
		Telemetry: s.telemetry,
		TraceID:   s.traceID,
		TurnID:    turnID,
	})
	if err != nil {
		return llm.ContentBlock{}, err
	}
	return ToolResultBlock(executed)
}

func (s *Session) executeConcurrentBatch(ctx context.Context, blocks []llm.ContentBlock, results []llm.ContentBlock, turnID string) error {
	if len(blocks) == 1 {
		result, err := s.executeSingleTool(ctx, blocks[0], turnID)
		if err != nil {
			return err
		}
		results[0] = result
		return nil
	}
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for idx, block := range blocks {
		idx, block := idx, block
		tctx := shallowCopyToolContext(s.toolContext, block.ID)
		g.Go(func() error {
			executed, err := s.executor.Execute(gctx, ExecuteRequest{
				Name:      block.Name,
				Input:     block.Input,
				ToolUseID: block.ID,
				Context:   tctx,
				Events:    s.toolEvents,
				Telemetry: s.telemetry,
				TraceID:   s.traceID,
				TurnID:    turnID,
			})
			if err != nil {
				return err
			}
			rb, err := ToolResultBlock(executed)
			if err != nil {
				return err
			}
			mu.Lock()
			results[idx] = rb
			mergeToolContextReadSet(s.toolContext, tctx)
			mu.Unlock()
			return nil
		})
	}
	return g.Wait()
}

func shallowCopyToolContext(tctx *tool.Context, toolUseID string) *tool.Context {
	if tctx == nil {
		return &tool.Context{ToolUseID: toolUseID}
	}
	c := *tctx
	c.ToolUseID = toolUseID
	c.ReadSet = cloneReadSet(tctx.ReadSet)
	c.Env = cloneStringMap(tctx.Env)
	c.Metadata = cloneMetadata(tctx.Metadata)
	return &c
}

func cloneReadSet(readSet map[string]tool.ReadFile) map[string]tool.ReadFile {
	if readSet == nil {
		return nil
	}
	cloned := make(map[string]tool.ReadFile, len(readSet))
	for k, v := range readSet {
		cloned[k] = v
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for k, v := range values {
		cloned[k] = v
	}
	return cloned
}

func cloneMetadata(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for k, v := range values {
		cloned[k] = v
	}
	return cloned
}

func mergeToolContextReadSet(dst *tool.Context, src *tool.Context) {
	if dst == nil || src == nil || src.ReadSet == nil {
		return
	}
	if dst.ReadSet == nil {
		dst.ReadSet = make(map[string]tool.ReadFile, len(src.ReadSet))
	}
	for k, v := range src.ReadSet {
		dst.ReadSet[k] = v
	}
}

func normalizeMaxIterations(value int) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: max iterations must be greater than or equal to 0", ErrInvalidSession)
	}
	if value == 0 {
		return DefaultMaxIterations, nil
	}
	return value, nil
}

func normalizeMaxHistoryMessages(value int) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("%w: max history messages must be greater than or equal to 0", ErrInvalidSession)
	}
	return value, nil
}

func cloneTools(tools []tool.Tool) []tool.Tool {
	if tools == nil {
		return nil
	}
	cloned := make([]tool.Tool, len(tools))
	copy(cloned, tools)
	return cloned
}

func userMessage(userText string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: userText}}}
}

func hasToolUse(message llm.Message) bool {
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeToolUse {
			return true
		}
	}
	return false
}

func cloneMessages(messages []llm.Message) []llm.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]llm.Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].Content = cloneContentBlocks(message.Content)
	}
	return cloned
}

func cloneContentBlocks(blocks []llm.ContentBlock) []llm.ContentBlock {
	if blocks == nil {
		return nil
	}
	cloned := make([]llm.ContentBlock, len(blocks))
	copy(cloned, blocks)
	for i := range cloned {
		cloned[i].Input = append(json.RawMessage(nil), cloned[i].Input...)
		cloned[i].Content = cloneContentBlocks(cloned[i].Content)
		if cloned[i].Source != nil {
			source := *cloned[i].Source
			source.Data = append([]byte(nil), cloned[i].Source.Data...)
			cloned[i].Source = &source
		}
	}
	return cloned
}

type blockAccumulator struct {
	block            llm.ContentBlock
	text             strings.Builder
	signature        strings.Builder
	inputJSON        []byte
	materializedText bool
}

func collectAssistantMessage(ctx context.Context, stream llm.Stream, delta func(string)) (llm.Message, llm.StopReason, *llm.Usage, error) {
	blocks := make(map[int]*blockAccumulator)
	var stopReason llm.StopReason
	var usage *llm.Usage

	for {
		select {
		case <-ctx.Done():
			return llm.Message{}, "", nil, ctx.Err()
		case event, ok := <-stream.Events():
			if !ok {
				if err := stream.Err(); err != nil {
					return llm.Message{}, "", nil, err
				}
				return llm.Message{Role: llm.RoleAssistant, Content: orderedBlocks(blocks)}, stopReason, usage, nil
			}
			if err := applyStreamEvent(blocks, event, delta); err != nil {
				return llm.Message{}, "", nil, err
			}
			if event.Type == llm.StreamEventTypeMessageStop {
				stopReason = event.StopReason
				usage = event.Usage
				return llm.Message{Role: llm.RoleAssistant, Content: orderedBlocks(blocks)}, stopReason, usage, nil
			}
		}
	}
}

func applyStreamEvent(blocks map[int]*blockAccumulator, event llm.StreamEvent, delta func(string)) error {
	switch event.Type {
	case llm.StreamEventTypeMessageStart, llm.StreamEventTypeMessageStop:
		return nil
	case llm.StreamEventTypeContentBlockStart:
		if event.Block == nil {
			return errors.New("content block start missing block")
		}
		blocks[event.Index] = &blockAccumulator{block: *event.Block}
		return nil
	case llm.StreamEventTypeContentBlockDelta:
		acc, ok := blocks[event.Index]
		if !ok {
			return fmt.Errorf("content block delta before start: index %d", event.Index)
		}
		if event.Delta == nil {
			return nil
		}
		acc.text.WriteString(event.Delta.Text)
		acc.text.WriteString(event.Delta.Thinking)
		acc.signature.WriteString(event.Delta.Signature)
		acc.inputJSON = append(acc.inputJSON, event.Delta.InputJSON...)
		if delta != nil && event.Delta.Text != "" && acc.block.Type == llm.ContentBlockTypeText {
			delta(event.Delta.Text)
		}
		return nil
	case llm.StreamEventTypeContentBlockStop:
		acc, ok := blocks[event.Index]
		if !ok {
			return fmt.Errorf("content block stop before start: index %d", event.Index)
		}
		materializeBlock(acc)
		return nil
	default:
		return nil
	}
}

func materializeBlock(acc *blockAccumulator) {
	if acc.materializedText {
		return
	}
	acc.block.Text += acc.text.String()
	acc.block.Signature += acc.signature.String()
	if len(acc.inputJSON) > 0 {
		acc.block.Input = json.RawMessage(acc.inputJSON)
	}
	acc.materializedText = true
}

func orderedBlocks(blocks map[int]*blockAccumulator) []llm.ContentBlock {
	if len(blocks) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	content := make([]llm.ContentBlock, 0, len(indexes))
	for _, index := range indexes {
		acc := blocks[index]
		materializeBlock(acc)
		content = append(content, acc.block)
	}
	return content
}
