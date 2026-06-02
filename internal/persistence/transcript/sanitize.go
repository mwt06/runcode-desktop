package transcript

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/wt68/runcode/pkg/llm"
)

type TurnInput struct {
	Time              time.Time
	SessionID         string
	TraceID           string
	TurnID            string
	CWD               string
	Model             string
	UserText          string
	FinalAssistant    llm.Message
	AssistantMessages []llm.Message
	ToolResults       []llm.ContentBlock
	StopReason        llm.StopReason
	Iterations        int
	Usage             *llm.Usage
}

func BuildTurnRecord(input TurnInput) TurnRecord {
	when := input.Time
	if when.IsZero() {
		when = time.Now().UTC()
	}
	return TurnRecord{
		Version:       1,
		Type:          "turn",
		Time:          when.UTC(),
		SessionID:     input.SessionID,
		TraceID:       input.TraceID,
		TurnID:        input.TurnID,
		CWD:           input.CWD,
		Model:         input.Model,
		UserText:      input.UserText,
		AssistantText: llm.TextContent(input.FinalAssistant),
		StopReason:    string(input.StopReason),
		Iterations:    input.Iterations,
		Usage:         input.Usage,
		ToolCalls:     toolCalls(input.AssistantMessages),
		ToolResults:   toolResults(input.ToolResults),
	}
}

func toolCalls(messages []llm.Message) []ToolCallSummary {
	var calls []ToolCallSummary
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type != llm.ContentBlockTypeToolUse {
				continue
			}
			call := ToolCallSummary{ID: block.ID, Name: block.Name}
			if strings.EqualFold(block.Name, "Bash") {
				call.Command = bashCommand(block.Input)
			}
			calls = append(calls, call)
		}
	}
	return calls
}

func bashCommand(input json.RawMessage) string {
	var fields struct {
		Command string `json:"command"`
	}
	if len(input) == 0 || json.Unmarshal(input, &fields) != nil {
		return ""
	}
	return fields.Command
}

func toolResults(blocks []llm.ContentBlock) []ToolResultSummary {
	var results []ToolResultSummary
	for _, block := range blocks {
		if block.Type != llm.ContentBlockTypeToolResult {
			continue
		}
		results = append(results, ToolResultSummary{
			ToolUseID:         block.ToolUseID,
			IsError:           block.IsError,
			ContentBlockCount: len(block.Content),
			TextBytes:         textBytes(block.Content),
		})
	}
	return results
}

func textBytes(blocks []llm.ContentBlock) int {
	total := 0
	for _, block := range blocks {
		if block.Type == llm.ContentBlockTypeText {
			total += len([]byte(block.Text))
		}
		total += textBytes(block.Content)
	}
	return total
}
