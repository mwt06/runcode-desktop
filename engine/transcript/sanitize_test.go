package transcript

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func TestBuildTurnRecordUsesWhitelistedFields(t *testing.T) {
	t.Parallel()

	record := BuildTurnRecord(TurnInput{
		Time:      time.Unix(1, 0),
		SessionID: "sess_test",
		TraceID:   "trace_1",
		TurnID:    "turn_1",
		CWD:       "/repo",
		Model:     "claude-test",
		UserText:  "hello",
		FinalAssistant: llm.Message{Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeText, Text: "hi"},
			{Type: llm.ContentBlockTypeThinking, Text: "private thinking"},
		}},
		StopReason: llm.StopReasonEndTurn,
		Iterations: 2,
		Usage:      &llm.Usage{InputTokens: 3, OutputTokens: 4},
	})

	if record.Version != 1 || record.Type != "turn" || record.SessionID != "sess_test" || record.AssistantText != "hi" {
		t.Fatalf("unexpected record: %#v", record)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if strings.Contains(string(data), "private thinking") {
		t.Fatalf("record leaked thinking content: %s", data)
	}
}

func TestBuildTurnRecordSummarizesToolCallsWithoutGenericInput(t *testing.T) {
	t.Parallel()

	record := BuildTurnRecord(TurnInput{
		SessionID: "sess_test",
		AssistantMessages: []llm.Message{{Content: []llm.ContentBlock{
			{Type: llm.ContentBlockTypeToolUse, ID: "read_1", Name: "Read", Input: json.RawMessage(`{"file_path":"secret.txt"}`)},
			{Type: llm.ContentBlockTypeToolUse, ID: "bash_1", Name: "Bash", Input: json.RawMessage(`{"command":"go test ./...","description":"secret description"}`)},
		}}},
	})

	if got, want := len(record.ToolCalls), 2; got != want {
		t.Fatalf("tool calls = %d, want %d", got, want)
	}
	if record.ToolCalls[0].Name != "Read" || record.ToolCalls[0].Command != "" {
		t.Fatalf("generic tool input leaked: %#v", record.ToolCalls[0])
	}
	if record.ToolCalls[1].Name != "Bash" || record.ToolCalls[1].Command != "go test ./..." {
		t.Fatalf("bash command not summarized: %#v", record.ToolCalls[1])
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	for _, forbidden := range []string{"secret.txt", "secret description"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("record leaked %q: %s", forbidden, data)
		}
	}
}

func TestBuildTurnRecordSummarizesToolResultsWithoutText(t *testing.T) {
	t.Parallel()

	record := BuildTurnRecord(TurnInput{
		SessionID: "sess_test",
		ToolResults: []llm.ContentBlock{{
			Type:      llm.ContentBlockTypeToolResult,
			ToolUseID: "tool_1",
			IsError:   true,
			Content: []llm.ContentBlock{
				{Type: llm.ContentBlockTypeText, Text: "secret output"},
				{Type: llm.ContentBlockTypeText, Text: "more"},
			},
		}},
	})

	if got, want := len(record.ToolResults), 1; got != want {
		t.Fatalf("tool results = %d, want %d", got, want)
	}
	result := record.ToolResults[0]
	if result.ToolUseID != "tool_1" || !result.IsError || result.ContentBlockCount != 2 || result.TextBytes != len([]byte("secret outputmore")) {
		t.Fatalf("unexpected summary: %#v", result)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if strings.Contains(string(data), "secret output") {
		t.Fatalf("record leaked tool output: %s", data)
	}
}
