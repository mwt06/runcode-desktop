package imagegentool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

type fakeHost struct {
	calls atomic.Int32
	block chan struct{}
	last  Input
	meta  CallMeta
	mu    sync.Mutex
}

func (h *fakeHost) Generate(_ context.Context, in Input, meta CallMeta) (protocol.ImageGenerationResult, error) {
	h.calls.Add(1)
	h.mu.Lock()
	h.last, h.meta = in, meta
	h.mu.Unlock()
	if h.block != nil {
		<-h.block
	}
	return protocol.ImageGenerationResult{
		Version: 1, GenerationID: "gen_1", Status: "succeeded", Model: "m", Size: in.Size, AccountingStatus: "unverified",
		Images: []protocol.ImageGenerationArtifact{{Path: "生成图片/gen_1/image.png", MIMEType: "image/png", Bytes: 10, Width: 1, Height: 1}},
	}, nil
}

func readySnapshot() Snapshot {
	return Snapshot{
		Available: true, SessionID: "sess", TurnID: "turn_1", Model: "image-model",
		Size: "1024x1024", SupportedSizes: []string{"1024x1024", "2048x2048"},
		BindingIdentity: "binding-a", Endpoint: "https://bridge.example/t/t1/v1/Images/Generations",
	}
}

func newTool(h Host, snap *Snapshot) *Tool {
	return &Tool{Host: h, SnapshotFunc: func() Snapshot { return *snap }}
}

func callCtx(toolUseID string) *tool.Context {
	return &tool.Context{SessionID: "sess", MessageID: "msg_1", ToolUseID: toolUseID}
}

func decodeResult(t *testing.T, r tool.Result) protocol.ImageGenerationResult {
	t.Helper()
	if len(r.Content) != 1 || r.Content[0].Type != tool.ResultContentTypeText {
		t.Fatalf("result content = %+v", r.Content)
	}
	if len(r.Content[0].Text) > MaxResultBytes {
		t.Fatalf("result %d bytes exceeds %d", len(r.Content[0].Text), MaxResultBytes)
	}
	var out protocol.ImageGenerationResult
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Version != 1 || out.AccountingStatus == "" || out.Images == nil {
		t.Fatalf("malformed result: %+v", out)
	}
	return out
}

func TestToolRejectsDisabledAndUnknownFields(t *testing.T) {
	long := strings.Repeat("图", MaxPromptRunes+1)
	cases := []struct {
		name  string
		snap  func(*Snapshot)
		input string
		tc    *tool.Context
		code  string
	}{
		{"unavailable", func(s *Snapshot) { s.Available = false }, `{"prompt":"海报"}`, callCtx("u1"), "not_configured"},
		{"not configured", func(s *Snapshot) { s.Available, s.Reason = false, "尚未选择生图模型" }, `{"prompt":"海报"}`, callCtx("u1"), "not_configured"},
		{"empty prompt", nil, `{"prompt":"   "}`, callCtx("u1"), "invalid_input"},
		{"long prompt", nil, `{"prompt":"` + long + `"}`, callCtx("u1"), "invalid_input"},
		{"extra model", nil, `{"prompt":"海报","model":"other"}`, callCtx("u1"), "invalid_input"},
		{"extra path", nil, `{"prompt":"海报","path":"C:/x.png"}`, callCtx("u1"), "invalid_input"},
		{"extra n", nil, `{"prompt":"海报","n":4}`, callCtx("u1"), "invalid_input"},
		{"malformed json", nil, `{"prompt":`, callCtx("u1"), "invalid_input"},
		{"missing tool use id", nil, `{"prompt":"海报"}`, &tool.Context{SessionID: "sess", MessageID: "msg_1"}, "invalid_input"},
		{"missing message id", nil, `{"prompt":"海报"}`, &tool.Context{SessionID: "sess", ToolUseID: "u1"}, "invalid_input"},
		{"no context", nil, `{"prompt":"海报"}`, nil, "invalid_input"},
		{"foreign session", nil, `{"prompt":"海报"}`, &tool.Context{SessionID: "other", MessageID: "msg_1", ToolUseID: "u1"}, "invalid_input"},
		{"missing turn", func(s *Snapshot) { s.TurnID = "" }, `{"prompt":"海报"}`, callCtx("u1"), "invalid_input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHost{}
			snap := readySnapshot()
			if tc.snap != nil {
				tc.snap(&snap)
			}
			r, err := newTool(h, &snap).Run(context.Background(), json.RawMessage(tc.input), tc.tc, nil)
			if err != nil {
				t.Fatalf("business rejection returned a Go error (could be retried): %v", err)
			}
			got := decodeResult(t, r)
			if !r.IsError || got.Status != "failed" || got.Code != tc.code || h.calls.Load() != 0 {
				t.Fatalf("got %+v isError=%v hostCalls=%d", got, r.IsError, h.calls.Load())
			}
		})
	}

	h := &fakeHost{}
	snap := readySnapshot()
	r, err := newTool(h, &snap).Run(context.Background(), json.RawMessage(`{"prompt":"  校园开放日海报  "}`), callCtx("u1"), nil)
	if err != nil || r.IsError || h.calls.Load() != 1 {
		t.Fatalf("valid call: %+v %v calls=%d", r, err, h.calls.Load())
	}
	if h.last.Prompt != "校园开放日海报" || h.last.Size != "1024x1024" {
		t.Fatalf("host input = %+v (want trimmed prompt and frozen default size)", h.last)
	}
	remapHost := &fakeHost{}
	remapSnap := readySnapshot()
	remapSnap.Size = ""
	remap, err := newTool(remapHost, &remapSnap).Run(context.Background(), json.RawMessage(`{"prompt":"海报","size":"4096x4096"}`), callCtx("u-remap"), nil)
	if err != nil || remap.IsError || remapHost.calls.Load() != 1 || remapHost.last.Size != "2048x2048" {
		t.Fatalf("unsupported size should remap: host=%+v result=%+v err=%v", remapHost.last, decodeResult(t, remap), err)
	}
	if note := decodeResult(t, remap).Message; note == "" || !strings.Contains(note, "2048x2048") {
		t.Fatalf("remap note missing: %q", note)
	}
	if h.meta != (CallMeta{SessionID: "sess", MessageID: "msg_1", ToolUseID: "u1", TurnID: "turn_1"}) {
		t.Fatalf("host meta = %+v", h.meta)
	}
}

func TestToolBudgetAndReplay(t *testing.T) {
	h := &fakeHost{}
	snap := readySnapshot()
	tl := newTool(h, &snap)
	input := json.RawMessage(`{"prompt":"海报","size":"2048x2048"}`)

	first, _ := tl.Run(context.Background(), input, callCtx("u1"), nil)
	replay, _ := tl.Run(context.Background(), input, callCtx("u1"), nil)
	if h.calls.Load() != 1 || first.Content[0].Text != replay.Content[0].Text {
		t.Fatalf("replay re-ran host: calls=%d", h.calls.Load())
	}
	other, _ := tl.Run(context.Background(), input, callCtx("u2"), nil)
	if got := decodeResult(t, other); got.Code != "turn_budget_exhausted" || !other.IsError || h.calls.Load() != 1 {
		t.Fatalf("second ToolUseID in the same turn: %+v calls=%d", got, h.calls.Load())
	}

	snap.TurnID = "turn_2"
	if r, _ := tl.Run(context.Background(), input, callCtx("u3"), nil); r.IsError || h.calls.Load() != 2 {
		t.Fatalf("new explicit user turn denied: calls=%d", h.calls.Load())
	}
	// An earlier turn's cached replay survives, but cannot spend the new turn.
	if r, _ := tl.Run(context.Background(), input, callCtx("u1"), nil); decodeResult(t, r).Code != "turn_budget_exhausted" || h.calls.Load() != 2 {
		t.Fatalf("old ToolUseID reused against the new turn: calls=%d", h.calls.Load())
	}
}

func TestToolConcurrentRunsEnterHostOnce(t *testing.T) {
	h := &fakeHost{block: make(chan struct{})}
	snap := readySnapshot()
	tl := newTool(h, &snap)
	var wg sync.WaitGroup
	results := make([]tool.Result, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _ = tl.Run(context.Background(), json.RawMessage(`{"prompt":"海报"}`), callCtx("u"+string(rune('a'+i))), nil)
		}(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	for h.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(h.block)
	wg.Wait()
	if h.calls.Load() != 1 {
		t.Fatalf("concurrent calls entered host %d times", h.calls.Load())
	}
	exhausted := 0
	for _, r := range results {
		if decodeResult(t, r).Code == "turn_budget_exhausted" {
			exhausted++
		}
	}
	if exhausted != 3 {
		t.Fatalf("exhausted = %d, want 3", exhausted)
	}
}

func TestSanitizeDisplayIntent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  高跟鞋  ", "高跟鞋"},
		{"一双\n新鞋\t在地毯上", "一双 新鞋 在地毯上"},
		{"海报 C:\\Users\\a\\x.png 完成", "海报 完成"},
		{"见 /tmp/out.png 与 D:/ws/a.jpg", "见 与"},
		{"C:/a.png", ""},
		{"只有路径 C:/a.png", "只有路径"},
		{strings.Repeat("画", MaxDisplayIntentRunes+10), strings.Repeat("画", MaxDisplayIntentRunes)},
	}
	for _, tc := range cases {
		if got := sanitizeDisplayIntent(tc.in); got != tc.want {
			t.Fatalf("sanitizeDisplayIntent(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestToolProgressAndResultCarryDisplayIntent(t *testing.T) {
	h := &fakeHost{}
	snap := readySnapshot()
	events := make(chan tool.Event, 8)
	prompt := "完整提示词不应出现在进度事件里，包含密钥TOKEN"
	raw := json.RawMessage(`{"prompt":"` + prompt + `","displayIntent":"一双\n高跟鞋 见 C:\\tmp\\a.png"}`)
	r, err := newTool(h, &snap).Run(context.Background(), raw, callCtx("u-intent"), events)
	if err != nil {
		t.Fatal(err)
	}
	close(events)
	wantIntent := "一双 高跟鞋 见"
	var progress []string
	for ev := range events {
		if ev.Type == tool.EventTypeProgress && ev.Message != "" && ev.Data == nil {
			progress = append(progress, ev.Message)
		}
	}
	if len(progress) == 0 || progress[0] != wantIntent {
		t.Fatalf("first progress = %v, want %q", progress, wantIntent)
	}
	for _, m := range progress {
		if strings.Contains(m, prompt) || strings.Contains(m, "TOKEN") || strings.Contains(m, "C:") {
			t.Fatalf("progress leaked prompt/path: %q", m)
		}
	}
	out := decodeResult(t, r)
	if out.DisplayIntent != wantIntent {
		t.Fatalf("result DisplayIntent = %q, want %q", out.DisplayIntent, wantIntent)
	}
	h.mu.Lock()
	gotIn := h.last
	h.mu.Unlock()
	if gotIn.DisplayIntent != wantIntent || gotIn.Prompt != prompt {
		t.Fatalf("host input = %+v", gotIn)
	}

	// 缺失意图时进度仍为默认文案，结果不带 DisplayIntent。
	events2 := make(chan tool.Event, 8)
	snap.TurnID = "turn_2"
	r2, _ := newTool(h, &snap).Run(context.Background(), json.RawMessage(`{"prompt":"海报"}`), callCtx("u-no-intent"), events2)
	close(events2)
	var first string
	for ev := range events2 {
		if ev.Type == tool.EventTypeProgress && ev.Data == nil {
			first = ev.Message
			break
		}
	}
	if first != "正在生成图片" {
		t.Fatalf("default progress = %q", first)
	}
	if decodeResult(t, r2).DisplayIntent != "" {
		t.Fatalf("unexpected DisplayIntent on missing input: %+v", decodeResult(t, r2))
	}
}

func TestToolPublishesResultAsEventData(t *testing.T) {
	h := &fakeHost{}
	snap := readySnapshot()
	events := make(chan tool.Event, 8)
	r, _ := newTool(h, &snap).Run(context.Background(), json.RawMessage(`{"prompt":"海报"}`), callCtx("u1"), events)
	close(events)
	var data *protocol.ImageGenerationResult
	for ev := range events {
		if res, ok := ev.Data.(protocol.ImageGenerationResult); ok {
			data = &res
		}
	}
	if data == nil || data.Status != "succeeded" {
		t.Fatal("result not published as tool event data")
	}
	if want := decodeResult(t, r); want.GenerationID != data.GenerationID {
		t.Fatalf("event data %+v differs from model content %+v", data, want)
	}
}

func TestParseInputFillsSmallestSquareWhenUnfixedAndOmitted(t *testing.T) {
	snap := readySnapshot()
	snap.Size = ""
	snap.SupportedSizes = []string{"2048x2048", "1024x1024", "1280x720"}
	got, err := parseInput(json.RawMessage(`{"prompt":"海报"}`), snap)
	if err != nil || got.Prompt != "海报" || got.Size != "1024x1024" || got.SizeNote != "" {
		t.Fatalf("omitted unfixed size should fill smallest square = %+v %v", got, err)
	}
	// 设置钉死默认时仍用设置值，不用最小正方形覆盖。
	snap.Size = "2048x2048"
	got, err = parseInput(json.RawMessage(`{"prompt":"海报"}`), snap)
	if err != nil || got.Size != "2048x2048" {
		t.Fatalf("fixed default size = %+v %v", got, err)
	}
	snap.Size = ""
	got, err = parseInput(json.RawMessage(`{"prompt":"海报","size":"2048x2048"}`), snap)
	if err != nil || got.Size != "2048x2048" {
		t.Fatalf("explicit size = %+v %v", got, err)
	}
	got, err = parseInput(json.RawMessage(`{"prompt":"海报","size":"4096x4096"}`), snap)
	if err != nil || got.Size != "2048x2048" || got.SizeNote == "" {
		t.Fatalf("unsupported size should remap closest = %+v %v", got, err)
	}
}

func TestParseInputForcesSingleSupportedSize(t *testing.T) {
	snap := readySnapshot()
	snap.Size = ""
	snap.SupportedSizes = []string{"1024x1024"}
	got, err := parseInput(json.RawMessage(`{"prompt":"海报"}`), snap)
	if err != nil || got.Size != "1024x1024" {
		t.Fatalf("single-size model must force size = %+v %v", got, err)
	}
	got, err = parseInput(json.RawMessage(`{"prompt":"海报","size":"2048x1024"}`), snap)
	if err != nil || got.Size != "1024x1024" || got.SizeNote == "" {
		t.Fatalf("single-size remap = %+v %v", got, err)
	}
}

func TestAuthorizationBindingPinsDestination(t *testing.T) {
	snap := readySnapshot()
	tl := newTool(&fakeHost{}, &snap)
	bind := func(input string) tool.AuthorizationBinding {
		t.Helper()
		b, err := tl.AuthorizationBinding(json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := bind(`{"prompt":"海报"}`)
	if base.Kind != BindingKind || base.Endpoint != snap.Endpoint || base.Identity == "" {
		t.Fatalf("binding = %+v", base)
	}
	if bind(`{"prompt":"另一张海报"}`).Identity != base.Identity {
		t.Fatal("prompt changed the destination identity")
	}
	if bind(`{"prompt":"海报","size":"1024x1024"}`).Identity != base.Identity {
		t.Fatal("explicit default size differs from omitted size")
	}
	if bind(`{"prompt":"海报","size":"2048x2048"}`).Identity == base.Identity {
		t.Fatal("different effective size shares an approval")
	}
	snap.BindingIdentity = "binding-b" // the host hashes model/tenant/account/TurnID/revision into this
	if bind(`{"prompt":"海报"}`).Identity == base.Identity {
		t.Fatal("different frozen destination shares an approval")
	}
	if strings.Contains(base.Identity, "binding-a") || strings.Contains(base.Endpoint, "Bearer") {
		t.Fatal("binding leaks raw configuration")
	}
	if _, err := tl.AuthorizationBinding(json.RawMessage(`{"prompt":`)); err == nil {
		t.Fatal("malformed input produced a binding")
	}
}

func TestAuthorizationBindingMatchesOutboundDefaultWhenUnfixed(t *testing.T) {
	snap := readySnapshot()
	snap.Size = ""
	snap.SupportedSizes = []string{"2048x2048", "1024x1024", "1280x720"}
	tl := newTool(&fakeHost{}, &snap)
	omit, err := tl.AuthorizationBinding(json.RawMessage(`{"prompt":"海报"}`))
	if err != nil {
		t.Fatal(err)
	}
	filled, err := tl.AuthorizationBinding(json.RawMessage(`{"prompt":"海报","size":"1024x1024"}`))
	if err != nil {
		t.Fatal(err)
	}
	if omit.Identity != filled.Identity {
		t.Fatal("unfixed omitted size must share approval identity with smallest-square outbound default")
	}
}

func TestToolMemoryStaysBounded(t *testing.T) {
	snap := readySnapshot()
	tl := newTool(&fakeHost{}, &snap)
	for i := 0; i < 3*maxRememberedTurns; i++ {
		snap.TurnID = "turn_" + string(rune('A'+i%26)) + strings.Repeat("x", i)
		_, _ = tl.Run(context.Background(), json.RawMessage(`{"prompt":"海报"}`), callCtx("u"), nil)
		_, _ = tl.Run(context.Background(), json.RawMessage(`{"prompt":" "}`), callCtx("bad"), nil)
	}
	if len(tl.turnOrder) > maxRememberedTurns || len(tl.known) > maxRememberedTurns || len(tl.replay) > 2*maxRememberedTurns {
		t.Fatalf("memory grew: order=%d known=%d replay=%d", len(tl.turnOrder), len(tl.known), len(tl.replay))
	}
}

func TestDescriptionStatesWhenToCallAndAvailability(t *testing.T) {
	snap := readySnapshot()
	tl := newTool(&fakeHost{}, &snap)
	desc := tl.Description()
	for _, want := range []string{"明确要求", "不要调用", "每回合最多一次", "支持尺寸", "image-model", "修改生图设置"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %s", want, desc)
		}
	}
	if len([]rune(desc)) > 520 {
		t.Fatalf("description too long (%d runes): %s", len([]rune(desc)), desc)
	}
	if strings.Contains(desc, "「+」") || strings.Contains(desc, "开启「生成图片」") {
		t.Fatalf("description still mentions composer toggle: %s", desc)
	}
	snap.Available, snap.Reason = false, "尚未选择生图模型"
	if d := tl.Description(); !strings.Contains(d, "请勿调用") || !strings.Contains(d, "修改生图设置") || strings.Contains(d, "image-model") {
		t.Fatalf("unavailable description: %s", d)
	}
}

// 格式路由由 renderRoutePrompt 独占；工具说明只回答“这个工具什么时候调用”。
func TestDescriptionDefersFormatRoutingToSystemPrompt(t *testing.T) {
	snap := readySnapshot()
	tl := newTool(&fakeHost{}, &snap)
	desc := tl.Description()
	for _, duplicate := range []string{"Mermaid", "SVG", "HTML", "流程图", "时序图", "架构图"} {
		if strings.Contains(desc, duplicate) {
			t.Fatalf("description duplicates system-prompt route %q: %s", duplicate, desc)
		}
	}
	snap.Available, snap.Reason = false, "尚未选择生图模型"
	d := tl.Description()
	if !strings.Contains(d, "请勿调用") || !strings.Contains(d, "尚未选择生图模型") {
		t.Fatalf("unavailable description must keep early exit: %s", d)
	}
	if strings.Contains(d, "image-model") {
		t.Fatalf("unavailable description must not list a ready model: %s", d)
	}
}

func TestInputSchemaDescriptionsStayFieldFocused(t *testing.T) {
	schema := InputSchema()
	size := schema.Properties["size"].Description
	if strings.Contains(size, "最接近") || strings.Contains(size, "最终回复") {
		t.Fatalf("size schema repeats tool-level strategy: %s", size)
	}
	if !strings.Contains(size, "支持尺寸") || !strings.Contains(size, "省略") {
		t.Fatalf("size schema missing field contract: %s", size)
	}
	if strings.Contains(schema.Properties["prompt"].Description, "Mermaid") {
		t.Fatalf("prompt schema leaked routing policy: %s", schema.Properties["prompt"].Description)
	}
}
