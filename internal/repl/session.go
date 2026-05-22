package repl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/prompt/sections"
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
}

type Session struct {
	provider      llm.Provider
	model         string
	tools         []tool.Tool
	executor      *Executor
	prompt        prompt.AssemblerOpts
	maxTokens     int
	temperature   *float64
	metadata      map[string]any
	toolContext   *tool.Context
	toolEvents    chan<- tool.Event
	maxIterations int
	reasoning     ReasoningOptions
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

	tools := cloneTools(opts.Tools)
	executor, err := NewExecutor(tools)
	if err != nil {
		return nil, err
	}

	return &Session{
		provider:      opts.Provider,
		model:         opts.Model,
		tools:         tools,
		executor:      executor,
		prompt:        opts.Prompt,
		maxTokens:     opts.MaxTokens,
		temperature:   opts.Temperature,
		metadata:      opts.Metadata,
		toolContext:   opts.ToolContext,
		toolEvents:    opts.ToolEvents,
		maxIterations: maxIterations,
		reasoning:     opts.Reasoning,
	}, nil
}

func (s *Session) RunTurn(ctx context.Context, userText string) (TurnResult, error) {
	messages := []llm.Message{userMessage(userText)}
	promptOpts := s.prompt
	var result TurnResult

	if s.reasoning.Enabled {
		classification, req, usage, err := s.classifyReasoningScenario(ctx, userText)
		if err != nil {
			if s.reasoning.Strict {
				return result, err
			}
			classification = ReasoningClassification{Scenario: defaultReasoningScenario(s.reasoning)}
		}
		result.ReasoningClassification = &classification
		result.ClassificationRequest = req
		result.ClassificationUsage = usage
		promptOpts.Reasoning = sections.ReasoningGuidance(string(classification.Scenario))
	}

	for iteration := 0; iteration < s.maxIterations; iteration++ {
		req, err := s.buildRequestWithMessagesAndPrompt(messages, promptOpts)
		if err != nil {
			return result, err
		}

		assistant, stopReason, usage, err := s.streamAssistant(ctx, req)
		if err != nil {
			return result, err
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
			return result, nil
		}
		if iteration == s.maxIterations-1 {
			return result, ErrMaxIterations
		}

		toolResults, err := s.executeToolUses(ctx, assistant)
		if err != nil {
			return result, err
		}
		toolMessage := llm.Message{Role: llm.RoleTool, Content: toolResults}
		result.LastToolMessage = &toolMessage
		result.ToolMessages = append(result.ToolMessages, toolMessage)
		result.ToolResults = append(result.ToolResults, toolResults...)
		messages = append(messages, toolMessage)
	}

	return result, ErrMaxIterations
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

func (s *Session) streamAssistant(ctx context.Context, req llm.Request) (llm.Message, llm.StopReason, *llm.Usage, error) {
	stream, err := s.provider.Stream(ctx, req)
	if err != nil {
		return llm.Message{}, "", nil, fmt.Errorf("stream provider: %w", err)
	}
	defer stream.Close()
	return collectAssistantMessage(ctx, stream)
}

func (s *Session) classifyReasoningScenario(ctx context.Context, userText string) (ReasoningClassification, *llm.Request, *llm.Usage, error) {
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
	assistant, _, usage, err := s.streamAssistant(ctx, req)
	if err != nil {
		return ReasoningClassification{}, &req, usage, err
	}
	classification, err := parseReasoningClassification(assistantText(assistant))
	if err != nil {
		return ReasoningClassification{}, &req, usage, err
	}
	return classification, &req, usage, nil
}

func (s *Session) executeToolUses(ctx context.Context, assistant llm.Message) ([]llm.ContentBlock, error) {
	var results []llm.ContentBlock
	for _, block := range assistant.Content {
		if block.Type != llm.ContentBlockTypeToolUse {
			continue
		}
		executed, err := s.executor.Execute(ctx, ExecuteRequest{
			Name:      block.Name,
			Input:     block.Input,
			ToolUseID: block.ID,
			Context:   s.toolContext,
			Events:    s.toolEvents,
		})
		if err != nil {
			return nil, err
		}
		resultBlock, err := ToolResultBlock(executed)
		if err != nil {
			return nil, err
		}
		results = append(results, resultBlock)
	}
	return results, nil
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

func cloneTools(tools []tool.Tool) []tool.Tool {
	if tools == nil {
		return nil
	}
	cloned := make([]tool.Tool, len(tools))
	copy(cloned, tools)
	return cloned
}

func assistantText(message llm.Message) string {
	var builder strings.Builder
	for _, block := range message.Content {
		if block.Type == llm.ContentBlockTypeText {
			builder.WriteString(block.Text)
		}
	}
	return builder.String()
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
		cloned[i].Content = cloneContentBlocks(cloned[i].Content)
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

func collectAssistantMessage(ctx context.Context, stream llm.Stream) (llm.Message, llm.StopReason, *llm.Usage, error) {
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
			if err := applyStreamEvent(blocks, event); err != nil {
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

func applyStreamEvent(blocks map[int]*blockAccumulator, event llm.StreamEvent) error {
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
