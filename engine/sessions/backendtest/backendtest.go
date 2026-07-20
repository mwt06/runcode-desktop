// Package backendtest is the acceptance suite for sessions.Backend
// implementations. Every implementation — the built-in JSONL and SQLite
// backends today, remote hot-tier/archive backends tomorrow — must pass Run:
// a port without a contract test is a port without a contract.
package backendtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
)

// Factory opens a fresh Backend over the same underlying storage on every
// call. Calling it twice must observe the same persisted data (reopen
// semantics); tests close every Backend they open.
type Factory func(t *testing.T) sessions.Backend

// Harness binds a Factory to fresh, isolated storage. It is invoked once per
// subtest so sessions created by one contract check never leak into another's
// List/Latest assertions.
type Harness func(t *testing.T) Factory

// Run exercises the full sessions.Backend contract against the given harness.
func Run(t *testing.T, harness Harness) {
	t.Run("AppendIsAtomicAndOrdered", func(t *testing.T) { testAppendOrdered(t, harness(t)) })
	t.Run("MissingSessionIsZero", func(t *testing.T) { testMissingSession(t, harness(t)) })
	t.Run("MetaRoundtrip", func(t *testing.T) { testMetaRoundtrip(t, harness(t)) })
	t.Run("ListDescribeLatest", func(t *testing.T) { testListDescribeLatest(t, harness(t)) })
	t.Run("DurableAcrossReopen", func(t *testing.T) { testReopen(t, harness(t)) })
	t.Run("ConcurrentAppendBatchesStayContiguous", func(t *testing.T) { testConcurrentAppend(t, harness(t)) })
	t.Run("CloseIsIdempotent", func(t *testing.T) { testCloseIdempotent(t, harness(t)) })
}

func userMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func assistantMsg(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: text}}}
}

func mustAppend(t *testing.T, s sessions.Store, msgs ...llm.Message) {
	t.Helper()
	if err := s.Append(context.Background(), msgs); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func openStore(t *testing.T, b sessions.Backend, id string) sessions.Store {
	t.Helper()
	store, err := b.OpenStore(context.Background(), id)
	if err != nil {
		t.Fatalf("OpenStore(%q): %v", id, err)
	}
	return store
}

func testAppendOrdered(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	defer b.Close(ctx)

	store := openStore(t, b, "sess_order")
	mustAppend(t, store, userMsg("q1"), assistantMsg("a1"))
	mustAppend(t, store, userMsg("q2"), assistantMsg("a2"))
	if err := store.Close(ctx); err != nil {
		t.Fatalf("store Close: %v", err)
	}

	got, err := b.LoadHistory(ctx, "sess_order")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	want := []string{"q1", "a1", "q2", "a2"}
	if len(got) != len(want) {
		t.Fatalf("history length = %d, want %d", len(got), len(want))
	}
	for i, text := range want {
		if llm.TextContent(got[i]) != text {
			t.Errorf("history[%d] = %q, want %q", i, llm.TextContent(got[i]), text)
		}
	}
}

func testMissingSession(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	defer b.Close(ctx)

	history, err := b.LoadHistory(ctx, "sess_missing")
	if err != nil || history != nil {
		t.Errorf("LoadHistory(missing) = (%v, %v), want (nil, nil)", history, err)
	}
	meta, err := b.LoadMeta(ctx, "sess_missing")
	if err != nil || !meta.IsZero() {
		t.Errorf("LoadMeta(missing) = (%+v, %v), want zero meta, nil error", meta, err)
	}
	if _, err := b.Describe(ctx, "sess_missing"); err == nil {
		t.Error("Describe(missing) succeeded, want error")
	}
}

func testMetaRoundtrip(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	defer b.Close(ctx)

	meta := sessions.SessionMeta{
		Model:          "claude-fable-5",
		PermissionMode: "judge",
		PlanMode:       true,
		ThinkingEffort: "high",
	}
	if err := b.SaveMeta(ctx, "sess_meta", meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	got, err := b.LoadMeta(ctx, "sess_meta")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if got != meta {
		t.Errorf("LoadMeta = %+v, want %+v", got, meta)
	}

	// Last write wins, whole-value replacement.
	updated := sessions.SessionMeta{Model: "other-model"}
	if err := b.SaveMeta(ctx, "sess_meta", updated); err != nil {
		t.Fatalf("SaveMeta(update): %v", err)
	}
	got, err = b.LoadMeta(ctx, "sess_meta")
	if err != nil {
		t.Fatalf("LoadMeta(update): %v", err)
	}
	if got != updated {
		t.Errorf("LoadMeta after update = %+v, want %+v", got, updated)
	}

	// Saving a zero meta clears the record.
	if err := b.SaveMeta(ctx, "sess_meta", sessions.SessionMeta{}); err != nil {
		t.Fatalf("SaveMeta(zero): %v", err)
	}
	got, err = b.LoadMeta(ctx, "sess_meta")
	if err != nil || !got.IsZero() {
		t.Errorf("LoadMeta after clear = (%+v, %v), want zero meta, nil error", got, err)
	}
}

func testListDescribeLatest(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	defer b.Close(ctx)

	first := openStore(t, b, "sess_first")
	mustAppend(t, first, userMsg("hello from first"), assistantMsg("reply"))
	first.Close(ctx)

	// Recency ordering is what this check asserts, so the two sessions must
	// carry distinguishable timestamps. File-backed implementations order by
	// mtime, whose granularity can swallow back-to-back writes (observed on
	// NTFS); give the clock a real tick between them.
	time.Sleep(20 * time.Millisecond)

	second := openStore(t, b, "sess_second")
	mustAppend(t, second, userMsg("hello from second"), assistantMsg("reply"))
	second.Close(ctx)

	infos, err := b.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("List returned %d sessions, want 2", len(infos))
	}
	if infos[0].ID != "sess_second" {
		t.Errorf("List[0].ID = %q, want most recent first (sess_second)", infos[0].ID)
	}

	info, err := b.Describe(ctx, "sess_first")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if info.ID != "sess_first" || info.Turns != 1 || info.Messages != 2 {
		t.Errorf("Describe = %+v, want ID sess_first, 1 turn, 2 messages", info)
	}

	latest, err := b.Latest(ctx)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest != "sess_second" {
		t.Errorf("Latest = %q, want sess_second", latest)
	}
}

func testReopen(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	store := openStore(t, b, "sess_durable")
	mustAppend(t, store, userMsg("before crash"), assistantMsg("saved"))
	meta := sessions.SessionMeta{PlanMode: true}
	if err := b.SaveMeta(ctx, "sess_durable", meta); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatalf("store Close: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatalf("backend Close: %v", err)
	}

	// A fresh backend over the same storage must see every committed Append
	// and the stored meta — the "node A closes, node B resumes" guarantee.
	reopened := open(t)
	defer reopened.Close(ctx)
	history, err := reopened.LoadHistory(ctx, "sess_durable")
	if err != nil {
		t.Fatalf("LoadHistory after reopen: %v", err)
	}
	if len(history) != 2 || llm.TextContent(history[0]) != "before crash" {
		t.Errorf("history after reopen = %d messages, want the 2 committed ones", len(history))
	}
	gotMeta, err := reopened.LoadMeta(ctx, "sess_durable")
	if err != nil || gotMeta != meta {
		t.Errorf("LoadMeta after reopen = (%+v, %v), want %+v", gotMeta, err, meta)
	}
}

func testConcurrentAppend(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	defer b.Close(ctx)

	const writers = 8
	const batches = 5
	store := openStore(t, b, "sess_conc")
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for batch := 0; batch < batches; batch++ {
				// Each batch is one atomic unit: a marker pair that must stay
				// adjacent in the stored history.
				head := userMsg(fmt.Sprintf("w%d-b%d-head", w, batch))
				tail := assistantMsg(fmt.Sprintf("w%d-b%d-tail", w, batch))
				if err := store.Append(ctx, []llm.Message{head, tail}); err != nil {
					t.Errorf("concurrent Append: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if err := store.Close(ctx); err != nil {
		t.Fatalf("store Close: %v", err)
	}

	history, err := b.LoadHistory(ctx, "sess_conc")
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != writers*batches*2 {
		t.Fatalf("history length = %d, want %d (no lost or duplicated batches)", len(history), writers*batches*2)
	}
	for i := 0; i < len(history); i += 2 {
		head := llm.TextContent(history[i])
		tail := llm.TextContent(history[i+1])
		if head[:len(head)-4]+"tail" != tail {
			t.Fatalf("batch torn at index %d: %q followed by %q", i, head, tail)
		}
	}
}

func testCloseIdempotent(t *testing.T, open Factory) {
	ctx := context.Background()
	b := open(t)
	store := openStore(t, b, "sess_close")
	mustAppend(t, store, userMsg("x"))
	if err := store.Close(ctx); err != nil {
		t.Fatalf("first store Close: %v", err)
	}
	if err := store.Close(ctx); err != nil {
		t.Errorf("second store Close: %v", err)
	}
	if err := b.Close(ctx); err != nil {
		t.Fatalf("backend Close: %v", err)
	}
}
