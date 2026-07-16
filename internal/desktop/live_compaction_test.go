package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/engine/llm"
)

// preserveDesktopConfig snapshots the real desktop.json and restores it after the
// test. Live tests drive App.StartSession, which persists its request via
// saveConfig — without this guard a test's throwaway budget/workspace would
// clobber the user's actual configuration (the exact 200k→2k regression this
// prevents). Restores the original bytes, or removes the file if none existed.
func preserveDesktopConfig(t *testing.T) {
	t.Helper()
	path := filepath.Join(os.Getenv("APPDATA"), "runcode", "desktop.json")
	orig, err := os.ReadFile(path)
	t.Cleanup(func() {
		if err != nil {
			_ = os.Remove(path)
			return
		}
		_ = os.WriteFile(path, orig, 0o600)
	})
}

// TestReopenCarriesConfiguredContextBudget mirrors reopening XRUN: it loads the
// persisted start request (as the form does) and starts a session from it, then
// asserts the session's reported budget equals the persisted one — so a restored
// 200k config yields a 200k session (no network; skipped when no config exists).
func TestReopenCarriesConfiguredContextBudget(t *testing.T) {
	path := filepath.Join(os.Getenv("APPDATA"), "runcode", "desktop.json")
	if _, err := os.Stat(path); err != nil {
		t.Skip("no desktop.json to reopen")
	}
	preserveDesktopConfig(t)
	app := New(&recordingSink{})
	req := app.LoadConfig()
	if strings.EqualFold(req.Provider, "passport") {
		// A passport session requires a live login (StartSession refuses without
		// one); this self-contained test can't provide that, so skip.
		t.Skip("persisted config is a passport session (needs live login)")
	}
	want := req.MaxContextTokens
	req.CWD = t.TempDir() // start in a scratch workspace, not the user's project
	info, err := app.StartSession(req)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer app.CloseSession()
	t.Logf("persisted budget %d → session reports %d", want, info.MaxContextTokens)
	if info.MaxContextTokens != want {
		t.Fatalf("session budget = %d, want persisted %d", info.MaxContextTokens, want)
	}
}

// waitForTurns blocks until the sink has recorded at least n EventTurnEnd events
// (or an EventTurnError), returning the collected TurnEnd payloads in order.
func waitForTurns(t *testing.T, sink *recordingSink, n int, deadline time.Time) []TurnEnd {
	t.Helper()
	for {
		var ends []TurnEnd
		sink.mu.Lock()
		for _, ev := range sink.events {
			switch ev.name {
			case EventTurnEnd:
				if te, ok := ev.data.(TurnEnd); ok {
					ends = append(ends, te)
				}
			case EventTurnError:
				sink.mu.Unlock()
				t.Fatalf("turn errored: %+v", ev.data)
			}
		}
		sink.mu.Unlock()
		if len(ends) >= n {
			return ends
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d turns (have %d)", n, len(ends))
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestLiveDesktopAutoCompaction drives several real turns through the App against
// the configured endpoint with a deliberately tiny context budget, so automatic
// compaction fires. It asserts the usage-bar data (TurnEnd.ContextTokens) is
// populated and that the working history ends up carrying a summary message —
// proving both the usage bar and auto-compaction end to end. Skipped unless
// RUNCODE_LIVE=1.
func TestLiveDesktopAutoCompaction(t *testing.T) {
	if os.Getenv("RUNCODE_LIVE") != "1" {
		t.Skip("set RUNCODE_LIVE=1 to run the live desktop compaction probe")
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
	// A tiny budget forces compaction: real input tokens (system prompt + history)
	// dwarf 2000, so every turn is over the 0.8 threshold; once >4 turns exist the
	// oldest are summarized.
	_, err = app.StartSession(StartSessionRequest{
		CWD:              t.TempDir(),
		Provider:         saved.Provider,
		Model:            saved.Model,
		BaseURL:          saved.BaseURL,
		APIKey:           saved.APIKey,
		AuthToken:        saved.AuthToken,
		PermissionMode:   "flight",
		MaxContextTokens: 2000,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	defer app.CloseSession()

	const turns = 7
	prompts := []string{
		"用一句话说明数字 1 的两倍是多少。", "用一句话说明数字 2 的两倍是多少。",
		"用一句话说明数字 3 的两倍是多少。", "用一句话说明数字 4 的两倍是多少。",
		"用一句话说明数字 5 的两倍是多少。", "用一句话说明数字 6 的两倍是多少。",
		"用一句话说明数字 7 的两倍是多少。",
	}
	deadline := time.Now().Add(240 * time.Second)
	for i := 0; i < turns; i++ {
		if err := app.SendMessage(prompts[i]); err != nil {
			t.Fatalf("SendMessage %d: %v", i, err)
		}
		waitForTurns(t, sink, i+1, deadline) // serialize: one turn at a time
	}

	ends := waitForTurns(t, sink, turns, deadline)
	// Usage bar: every turn should report a positive context occupancy.
	populated := 0
	for _, e := range ends {
		if e.ContextTokens > 0 {
			populated++
		}
	}
	t.Logf("contextTokens per turn: %v", contextTokensOf(ends))
	if populated == 0 {
		t.Fatal("no turn reported ContextTokens > 0 — usage bar would stay empty")
	}

	// Auto-compaction: the working history should now begin with / contain a summary
	// message condensing the earliest turns.
	history := app.session.History()
	if !hasSummaryMessage(history) {
		t.Fatalf("expected a compaction summary in history after %d turns, found none (len=%d)", turns, len(history))
	}
	t.Logf("history length after %d turns = %d messages (compacted, summary present)", turns, len(history))
}

func contextTokensOf(ends []TurnEnd) []int {
	out := make([]int, len(ends))
	for i, e := range ends {
		out[i] = e.ContextTokens
	}
	return out
}

func hasSummaryMessage(history []llm.Message) bool {
	for _, m := range history {
		for _, b := range m.Content {
			if b.Type == llm.ContentBlockTypeText && strings.Contains(b.Text, "Summary of earlier conversation") {
				return true
			}
		}
	}
	return false
}
