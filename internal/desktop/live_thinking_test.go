package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/protocol"
)

// TestLiveDesktopEmitsThinking drives a full turn through the real App (engine +
// provider + event sink) against the configured endpoint, with thinking effort on
// and a prompt that actually requires reasoning, and asserts that
// EventAssistantThinking events reach the sink. It is skipped unless
// RUNCODE_LIVE=1 so ordinary test runs never hit the network.
func TestLiveDesktopEmitsThinking(t *testing.T) {
	if os.Getenv("RUNCODE_LIVE") != "1" {
		t.Skip("set RUNCODE_LIVE=1 to run the live desktop probe")
	}
	preserveDesktopConfig(t)
	raw, err := os.ReadFile(filepath.Join(os.Getenv("APPDATA"), "runcode", "desktop.json"))
	if err != nil {
		t.Fatalf("read desktop.json: %v", err)
	}
	var saved StartSessionRequest
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("parse desktop.json: %v", err)
	}

	sink := &recordingSink{}
	app := New(sink)
	req := StartSessionRequest{
		CWD:            t.TempDir(),
		Provider:       saved.Provider,
		Model:          saved.Model,
		BaseURL:        saved.BaseURL,
		APIKey:         saved.APIKey,
		AuthToken:      saved.AuthToken,
		PermissionMode: "flight",
		ThinkingEffort: "high",
	}
	if _, err := app.StartSession(req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer app.CloseSession()

	if err := app.SendMessage("9.11 和 9.9 哪个大？一步步推理再回答。"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// Wait for the turn to finish (or fail) with a generous timeout.
	deadline := time.Now().Add(90 * time.Second)
	for {
		if _, ok := sink.lastOf(EventTurnEnd); ok {
			break
		}
		if ev, ok := sink.lastOf(EventTurnError); ok {
			t.Fatalf("turn errored: %+v", ev.data)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for turn to end")
		}
		time.Sleep(200 * time.Millisecond)
	}

	var thinkingChars, deltaChars int
	sink.mu.Lock()
	for _, ev := range sink.events {
		env, ok := ev.data.(protocol.Envelope)
		if !ok {
			continue
		}
		if d, ok := env.Payload.(AssistantDelta); ok {
			switch ev.name {
			case EventAssistantThinking:
				thinkingChars += len(d.Text)
			case EventAssistantDelta:
				deltaChars += len(d.Text)
			}
		}
	}
	sink.mu.Unlock()
	t.Logf("desktop emitted thinking=%d answer=%d chars", thinkingChars, deltaChars)
	if thinkingChars == 0 {
		t.Fatal("expected EventAssistantThinking events, got none — desktop thinking chain is broken")
	}
}
