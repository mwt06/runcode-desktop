package localusage

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

type fakeProvider struct {
	name   string
	stream func(context.Context, llm.Request) (llm.Stream, error)
}

func (f fakeProvider) Name() string                   { return f.name }
func (f fakeProvider) Capabilities() llm.Capabilities { return llm.Capabilities{} }
func (f fakeProvider) Stream(ctx context.Context, req llm.Request) (llm.Stream, error) {
	return f.stream(ctx, req)
}

type memStream struct {
	ch   chan llm.StreamEvent
	err  error
	once sync.Once
}

// nonClosingStream models a provider whose Close stops production but leaves
// its Events channel open. The wrapper must still stop its pump through done.
type nonClosingStream struct {
	ch     chan llm.StreamEvent
	closed chan struct{}
	once   sync.Once
}

func (s *nonClosingStream) Events() <-chan llm.StreamEvent { return s.ch }
func (s *nonClosingStream) Err() error                     { return nil }
func (s *nonClosingStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestLocalUsageCloseStopsPumpWhenInnerEventsRemainOpen(t *testing.T) {
	// The consumer reads one event and then closes. The inner stream's channel
	// stays open, so only the wrapper's done signal can release the pump.
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("close-open-events")
	inner := &nonClosingStream{ch: make(chan llm.StreamEvent), closed: make(chan struct{})}
	base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) {
		return inner, nil
	}}
	tracker := NewRequestPurposeTracker()
	p := WrapPlatformProviderObserved(base, func(context.Context) (Scope, error) { return scope, nil }, store, tracker, CallKindLLM)
	st, err := p.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	out := st.Events()
	inner.ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStart}
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("wrapper did not forward the first event")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("wrapper emitted an event after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("wrapper pump did not stop after Close")
	}
	select {
	case <-inner.closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not stop the inner stream")
	}
}

func TestLocalUsageCloseBeforeEvents(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("close-before-events")
	inner := &nonClosingStream{ch: make(chan llm.StreamEvent), closed: make(chan struct{})}
	base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) { return inner, nil }}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	st, err := p.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-st.Events():
		if ok {
			t.Fatal("closed stream returned an event")
		}
	case <-time.After(time.Second):
		t.Fatal("Events after Close left the pump running")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	var finishes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE phase='finish'`).Scan(&finishes); err != nil {
		t.Fatal(err)
	}
	if finishes != 1 {
		t.Fatalf("finishes=%d want 1", finishes)
	}
}

func TestLocalUsageCloseAfterOneEventDoesNotLeakPumps(t *testing.T) {
	// Count only wrapper pumps: unrelated runtime goroutines can legitimately
	// change, but every pump started here must exit without draining its output.
	pumpCount := func() int {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		return bytes.Count(buf[:n], []byte("localusage.(*countingStream).pump("))
	}
	baseline := pumpCount()
	store, err := Open(filepath.Join(t.TempDir(), "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("early-close-many-events")
	const streams = 32
	var outputs []<-chan llm.StreamEvent
	t.Cleanup(func() {
		// Release a broken implementation's blocked pumps after reporting the
		// failure, so the regression test itself does not leak goroutines.
		for _, out := range outputs {
			for range out {
			}
		}
	})
	base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) {
		inner := &memStream{ch: make(chan llm.StreamEvent, 1024)}
		for i := 0; i < cap(inner.ch); i++ {
			inner.ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStart}
		}
		return inner, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	for i := 0; i < streams; i++ {
		st, err := p.Stream(context.Background(), llm.Request{Model: "m"})
		if err != nil {
			t.Fatal(err)
		}
		out := st.Events()
		outputs = append(outputs, out)
		select {
		case <-out:
		case <-time.After(time.Second):
			t.Fatal("wrapper did not forward the first event")
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for pumpCount() > baseline && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := pumpCount(); got > baseline {
		t.Fatalf("wrapper pumps after closing %d streams=%d, initial=%d", streams, got, baseline)
	}
	var finishes int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE phase='finish'`).Scan(&finishes); err != nil {
		t.Fatal(err)
	}
	if finishes != streams {
		t.Fatalf("finishes=%d want %d", finishes, streams)
	}
}

func (m *memStream) Events() <-chan llm.StreamEvent { return m.ch }
func (m *memStream) Err() error                     { return m.err }
func (m *memStream) Close() error {
	m.once.Do(func() { close(m.ch) })
	return nil
}

func TestLocalUsageProviderFinalUsage(t *testing.T) {
	// U01：多次 usage 取最终快照；重复包装只计一次。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("prov")
	base := fakeProvider{name: "openai", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 4)
		s := &memStream{ch: ch}
		go func() {
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStart, Usage: &llm.Usage{InputTokens: 1, OutputTokens: 1}}
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, Usage: &llm.Usage{InputTokens: 100, OutputTokens: 20}}
			_ = s.Close()
		}()
		return s, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	p = WrapPlatformProvider(p, func(context.Context) (Scope, error) { return scope, nil }, store) // 幂等
	st, err := p.Stream(context.Background(), llm.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
		continue // 排空事件流
	}
	_ = st.Close()
	// 等 finish
	time.Sleep(20 * time.Millisecond)
	n, _ := store.CountCalls(context.Background(), scope.ScopeID)
	if n != 1 {
		t.Fatalf("calls=%d want 1", n)
	}
	var in, out int64
	var inK, outK int
	err = store.db.QueryRow(`SELECT input_tokens, input_tokens_known, output_tokens, output_tokens_known
		FROM events WHERE phase='finish'`).Scan(&in, &inK, &out, &outK)
	if err != nil {
		t.Fatal(err)
	}
	if in != 100 || out != 20 || inK != 1 || outK != 1 {
		t.Fatalf("usage in=%d/%d out=%d/%d", in, inK, out, outK)
	}
}

func TestLocalUsageMissingUsage(t *testing.T) {
	// U02：无 usage → Token 未知（null／known=0）。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("miss")
	base := fakeProvider{name: "openai", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		s := &memStream{ch: ch}
		go func() {
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop}
			_ = s.Close()
		}()
		return s, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	st, _ := p.Stream(context.Background(), llm.Request{Model: "m"})
	for range st.Events() {
		continue // 排空事件流
	}
	_ = st.Close()
	time.Sleep(20 * time.Millisecond)
	var inK, outK int
	_ = store.db.QueryRow(`SELECT input_tokens_known, output_tokens_known FROM events WHERE phase='finish'`).Scan(&inK, &outK)
	if inK != 0 || outK != 0 {
		t.Fatalf("expected unknown tokens, got known in=%d out=%d", inK, outK)
	}
	// 明确 known-zero 走 Store.Finish（生产 OpenAI 无 presence 时不把缺省 0 当已知）
	start := sampleStart("zero", scope, time.Now().UTC())
	_ = store.Begin(context.Background(), start)
	z := int64(0)
	_ = store.Finish(context.Background(), FinishRecord{
		CallID: "zero", Status: StatusSucceeded, FinishedAt: time.Now().UTC(),
		InputTokens: &z, InputTokensKnown: true, OutputTokens: &z, OutputTokensKnown: true,
		TokenSource: "reported", FrozenStart: start,
	})
	var zin int64
	var zinK int
	_ = store.db.QueryRow(`SELECT input_tokens, input_tokens_known FROM events WHERE call_id='zero' AND phase='finish'`).Scan(&zin, &zinK)
	if zin != 0 || zinK != 1 {
		t.Fatalf("known zero failed: %d/%d", zin, zinK)
	}
}

func TestLocalUsageStreamErrorIsNotRecordedAsSuccess(t *testing.T) {
	// 底层流在发送完事件后失败，泵结束也必须保留 unknown 终态。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("stream-error")
	streamErr := context.DeadlineExceeded
	base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		s := &memStream{ch: ch}
		go func() {
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop}
			s.err = streamErr
			_ = s.Close()
		}()
		return s, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	st, err := p.Stream(context.Background(), llm.Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
		continue // 排空事件流
	}
	var got string
	if err := store.db.QueryRow(`SELECT status FROM events WHERE phase='finish'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got == StatusSucceeded {
		t.Fatalf("stream error was recorded as %q", got)
	}
	if got != StatusUnknown {
		t.Fatalf("stream error status=%q want %q", got, StatusUnknown)
	}
}

func TestLocalUsageStreamCloseRace(t *testing.T) {
	// U17：Close 与泵结束竞争，终态最多一次。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("race")
	base := fakeProvider{name: "openai", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent)
		s := &memStream{ch: ch}
		go func() {
			time.Sleep(5 * time.Millisecond)
			_ = s.Close()
		}()
		return s, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) { return scope, nil }, store)
	st, _ := p.Stream(context.Background(), llm.Request{Model: "m"})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range st.Events() {
			continue // 排空事件流
		}
	}()
	go func() { defer wg.Done(); _ = st.Close() }()
	wg.Wait()
	time.Sleep(30 * time.Millisecond)
	var finishes int
	_ = store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE phase='finish'`).Scan(&finishes)
	if finishes != 1 {
		t.Fatalf("finishes=%d want 1", finishes)
	}
}

func TestLocalUsagePlatformOnly(t *testing.T) {
	// 自定义路径：不包装则零记录。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := fakeProvider{name: "custom", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent)
		s := &memStream{ch: ch}
		go func() { _ = s.Close() }()
		return s, nil
	}}
	// 不 wrap
	st, _ := base.Stream(context.Background(), llm.Request{Model: "x"})
	for range st.Events() {
		continue // 排空事件流
	}
	_ = st.Close()
	n, _ := store.CountCalls(context.Background(), "any")
	if n != 0 {
		t.Fatalf("custom path should not record, got %d", n)
	}
}

func TestLocalUsageSkipsAuxiliaryTitleStream(t *testing.T) {
	// 自动标题与主回合各一次 Stream：标题不得入账，否则用量页「请求数」翻倍。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := sampleScope("aux-title")
	base := fakeProvider{name: "openai", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		s := &memStream{ch: ch}
		go func() {
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, Usage: &llm.Usage{InputTokens: 79, OutputTokens: 12}}
			_ = s.Close()
		}()
		return s, nil
	}}
	tracker := NewRequestPurposeTracker()
	p := WrapPlatformProviderObserved(base, func(context.Context) (Scope, error) { return scope, nil }, store, tracker, CallKindLLM)
	titleReq := llm.Request{
		Model: "qwen3.6-27b",
		System: []llm.ContentBlock{{
			Type: llm.ContentBlockTypeText,
			Text: "You name a coding-assistant conversation. Given the user's request, reply with a concise title.",
		}},
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "Title this request:\n\nwho are yo"}},
		}},
		MaxTokens: 2048,
	}
	tracker.Observe("session_title", titleReq)
	st, err := p.Stream(context.Background(), titleReq)
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
		continue
	}
	_ = st.Close()
	time.Sleep(20 * time.Millisecond)
	n, _ := store.CountCalls(context.Background(), scope.ScopeID)
	if n != 0 {
		t.Fatalf("title stream recorded %d calls, want 0", n)
	}
	mainReq := llm.Request{
		Model: "qwen3.6-27b",
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "who are yo"}},
		}},
		Tools: []llm.ToolSpec{{Name: "Bash", Description: "run"}},
	}
	tracker.Observe("assistant", mainReq)
	st, err = p.Stream(context.Background(), mainReq)
	if err != nil {
		t.Fatal(err)
	}
	for range st.Events() {
		continue
	}
	_ = st.Close()
	time.Sleep(20 * time.Millisecond)
	n, _ = store.CountCalls(context.Background(), scope.ScopeID)
	if n != 1 {
		t.Fatalf("main stream calls=%d want 1", n)
	}
}

func TestLocalUsageUsesObservedPurpose(t *testing.T) {
	store := openTemp(t, nil)
	scope := sampleScope("purpose")
	base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		stream := &memStream{ch: ch}
		ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 2}}
		_ = stream.Close()
		return stream, nil
	}}
	tracker := NewRequestPurposeTracker()
	provider := WrapPlatformProviderObserved(base, func(context.Context) (Scope, error) { return scope, nil }, store, tracker, CallKindLLM)

	title := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "unmatched wording"}}}}}
	tracker.Observe("session_title", title)
	stream, err := provider.Stream(context.Background(), title)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
		continue
	}
	_ = stream.Close()

	compaction := llm.Request{Model: "m", Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: "also unmatched"}}}}}
	tracker.Observe("compaction_summary", compaction)
	stream, err = provider.Stream(context.Background(), compaction)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events() {
		continue
	}
	_ = stream.Close()

	page, err := store.QueryPage(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 1 || page.Calls[0].Model != "m" {
		t.Fatalf("purpose-filtered calls = %+v", page)
	}
}

func TestLocalUsageAuxiliaryFilteringAppliesOnlyToLLM(t *testing.T) {
	for _, tc := range []struct {
		name, kind, purpose, system, question string
		want                                  int
	}{
		{"ordinary_tool_free_chat", CallKindLLM, "assistant", "You are a helpful assistant.", "What is this?", 1},
		{"title", CallKindLLM, "session_title", "unmatched", "unmatched", 0},
		{"compaction", CallKindLLM, "compaction_summary", "unmatched", "unmatched", 1},
		{"vision", CallKindVision, "image_analysis", "Analyze the image.", "question", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTemp(t, nil)
			scope := sampleScope(tc.name)
			base := fakeProvider{name: "openai", stream: func(context.Context, llm.Request) (llm.Stream, error) {
				ch := make(chan llm.StreamEvent)
				stream := &memStream{ch: ch}
				_ = stream.Close()
				return stream, nil
			}}
			tracker := NewRequestPurposeTracker()
			provider := WrapPlatformProviderObserved(base, func(context.Context) (Scope, error) { return scope, nil }, store, tracker, tc.kind)
			req := llm.Request{Model: "m", System: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: tc.system}}, Messages: []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.ContentBlockTypeText, Text: tc.question}}}}}
			tracker.Observe(tc.purpose, req)
			stream, err := provider.Stream(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events() {
				continue
			}
			_ = stream.Close()
			page, err := store.QueryPage(context.Background(), QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
			if err != nil || page.TotalCount != tc.want {
				t.Fatalf("usage=%+v error=%v want=%d", page, err, tc.want)
			}
		})
	}
}

func TestLocalUsageIdentityFrozen(t *testing.T) {
	// U07：Stream 开始冻结 scope；中途 freeze 变了也不改归属。
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "u.sqlite3"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	a := sampleScope("freeze-a")
	b := Scope{ScopeID: "scope-b", AccountDigest: "b", BridgeDigest: "br", TenantID: "t2"}
	cur := a
	var mu sync.Mutex
	base := fakeProvider{name: "openai", stream: func(_ context.Context, _ llm.Request) (llm.Stream, error) {
		ch := make(chan llm.StreamEvent, 1)
		s := &memStream{ch: ch}
		go func() {
			mu.Lock()
			cur = b // 模拟登录切换
			mu.Unlock()
			ch <- llm.StreamEvent{Type: llm.StreamEventTypeMessageStop, Usage: &llm.Usage{InputTokens: 3, OutputTokens: 1}}
			_ = s.Close()
		}()
		return s, nil
	}}
	p := WrapPlatformProvider(base, func(context.Context) (Scope, error) {
		mu.Lock()
		defer mu.Unlock()
		return cur, nil
	}, store)
	st, _ := p.Stream(context.Background(), llm.Request{Model: "m"})
	for range st.Events() {
		continue // 排空事件流
	}
	_ = st.Close()
	time.Sleep(20 * time.Millisecond)
	na, _ := store.CountCalls(context.Background(), a.ScopeID)
	nb, _ := store.CountCalls(context.Background(), b.ScopeID)
	if na != 1 || nb != 0 {
		t.Fatalf("frozen scope broken: a=%d b=%d", na, nb)
	}
}
