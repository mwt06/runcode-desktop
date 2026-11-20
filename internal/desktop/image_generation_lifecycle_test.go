package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/imagegentool"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

func TestImageGenerationLifecycleFrozenTargetSurvivesSettingsChange(t *testing.T) {
	f := newImageGenFlow(t)
	// 调用尚在审批／执行时用户另存默认：当前回合必须仍用冻结目标。
	saveImageModel(t, f.app, "tenant-a", "image-only-url", "2048x2048")
	if got := f.run(t, "tu_1", `{"prompt":"海报"}`); got.Status != "succeeded" || f.bridge.models.Load() != "image-only-b64" {
		t.Fatalf("running turn changed model: %+v model=%v", got, f.bridge.models.Load())
	}
	// 下一个独立回合才改用新默认。
	f.app.freezeImageGenerationTurn(f.entry)
	if snap := f.state.snapshot(); snap.Model != "image-only-url" || snap.Size != "2048x2048" {
		t.Fatalf("next turn = %+v", snap)
	}
}

func TestImageGenerationLifecycleInjectedMessagesKeepTheBudget(t *testing.T) {
	f := newImageGenFlow(t)
	if got := f.run(t, "tu_1", `{"prompt":"海报"}`); got.Status != "succeeded" {
		t.Fatalf("first = %+v", got)
	}
	// 注入的「再生成一张」仍属同一回合：不重冻、不加预算。
	if got := f.run(t, "tu_2", `{"prompt":"再来一张"}`); got.Code != "turn_budget_exhausted" {
		t.Fatalf("injected request = %+v", got)
	}
	// 同回合重建工具实例（或进程重启）须被持久回合标记拒绝。
	other := f.app.newImageGenerationTool(f.state)
	snap := f.state.snapshot()
	token := imagegentool.NewInvocationToken(f.id, snap.Generation, "q_tu_3", snap.TurnID)
	token.MarkPersisted(nil)
	r, _ := other.Run(imagegentool.WithInvocation(context.Background(), token), json.RawMessage(`{"prompt":"再来一张"}`), &tool.Context{SessionID: f.id, ToolUseID: "tu_3"}, nil)
	token.Close()
	if !r.IsError || f.bridge.posts.Load() != 1 {
		t.Fatalf("second tool instance resubmitted: posts=%d", f.bridge.posts.Load())
	}
	f.app.freezeImageGenerationTurn(f.entry)
	if got := f.run(t, "tu_4", `{"prompt":"新的一轮"}`); got.Status != "succeeded" || f.bridge.posts.Load() != 2 {
		t.Fatalf("next independent turn = %+v posts=%d", got, f.bridge.posts.Load())
	}
}

func TestImageGenerationLifecycleAuthoritativeChecksBeforeSubmit(t *testing.T) {
	cases := []struct {
		name string
		prep func(*imageGenFlow)
	}{
		{"model withdrawn after approval", func(f *imageGenFlow) { f.bridge.dropModel(t, "image-only-b64") }},
		{"account switched", func(f *imageGenFlow) {
			f.app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-b"), Expiry: time.Now().Add(time.Hour)})
		}},
		{"logged out", func(f *imageGenFlow) { f.app.tokens.Clear() }},
		{"session closed", func(f *imageGenFlow) {
			f.app.mu.Lock()
			f.entry.closed = true
			f.app.mu.Unlock()
		}},
		{"session instance replaced", func(f *imageGenFlow) {
			f.app.mu.Lock()
			f.entry.imageGeneration = newImageGenerationTurnState(f.id, "instance-2")
			f.app.mu.Unlock()
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newImageGenFlow(t)
			tc.prep(f)
			got := f.run(t, "tu_1", `{"prompt":"海报"}`)
			if got.Status != "failed" || f.bridge.posts.Load() != 0 {
				t.Fatalf("result = %+v posts=%d (Core POST must be 0)", got, f.bridge.posts.Load())
			}
		})
	}
}

func TestImageGenerationLifecycleUnknownIsNotRetried(t *testing.T) {
	f := newImageGenFlow(t)
	f.bridge.fail.Store(http.StatusBadGateway)
	got := f.run(t, "tu_1", `{"prompt":"海报"}`)
	if got.Status != "unknown" || got.SubmissionStatus != "attempted" || f.bridge.posts.Load() != 1 {
		t.Fatalf("result = %+v posts=%d", got, f.bridge.posts.Load())
	}
	f.bridge.fail.Store(0)
	if again := f.run(t, "tu_2", `{"prompt":"海报"}`); again.Code != "turn_budget_exhausted" || f.bridge.posts.Load() != 1 {
		t.Fatalf("unknown outcome retried: %+v posts=%d", again, f.bridge.posts.Load())
	}
	rec, err := readImageGenerationLedger(f.cwd, f.id, got.GenerationID)
	if err != nil || rec.Status != "unknown" || rec.SubmissionStatus != "attempted" || rec.AccountingStatus != "unverified" {
		t.Fatalf("ledger = %+v %v", rec, err)
	}
}

func TestImageGenerationLifecycleCancelKeepsBudgetAndSaysWhy(t *testing.T) {
	f := newImageGenFlow(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snap := f.state.snapshot()
	token := imagegentool.NewInvocationToken(f.id, snap.Generation, "q_tu_1", snap.TurnID)
	token.MarkPersisted(nil)
	r, _ := f.tool.Run(imagegentool.WithInvocation(ctx, token), json.RawMessage(`{"prompt":"海报"}`), &tool.Context{SessionID: f.id, ToolUseID: "tu_1"}, nil)
	token.Close()
	var got struct {
		Status, Message string
	}
	_ = json.Unmarshal([]byte(r.Content[0].Text), &got)
	if got.Status == "succeeded" {
		t.Fatal("cancelled call reported success")
	}
	if f.bridge.posts.Load() > 1 {
		t.Fatalf("posts = %d", f.bridge.posts.Load())
	}
	if again := f.run(t, "tu_2", `{"prompt":"海报"}`); again.Code != "turn_budget_exhausted" && f.bridge.posts.Load() != 0 {
		t.Fatalf("cancel released a budget that may have been spent: %+v", again)
	}
}

func TestImageGenerationLifecycleSaveFailureIsNotSuccessOrRetry(t *testing.T) {
	f := newImageGenFlow(t)
	if err := os.WriteFile(filepath.Join(f.cwd, imageGenerationDir), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := f.run(t, "tu_1", `{"prompt":"海报"}`)
	if got.Status != "save_failed" || got.SubmissionStatus != "attempted" || got.Code != "save_failed" || len(got.Images) != 0 || got.RequestID != "brg-synthetic-0002" ||
		got.AccountingStatus != "unverified" || !strings.Contains(got.Message, "计费待核验") || strings.Contains(got.Message, "免费") {
		t.Fatalf("save failure = %+v", got)
	}
	if again := f.run(t, "tu_2", `{"prompt":"海报"}`); again.Code != "turn_budget_exhausted" || f.bridge.posts.Load() != 1 {
		t.Fatalf("save failure triggered another POST: %+v posts=%d", again, f.bridge.posts.Load())
	}
	rec, err := readImageGenerationLedger(f.cwd, f.id, got.GenerationID)
	if err != nil || rec.Status != "save_failed" || rec.SubmissionStatus != "attempted" {
		t.Fatalf("ledger = %+v %v", rec, err)
	}
}

func TestImageGenerationLifecycleFreezeNeverTouchesTheNetwork(t *testing.T) {
	f := newImageGenFlow(t)
	before := f.bridge.posts.Load()
	f.bridge.srv.Close() // any catalog or generation request would now fail loudly
	for i := 0; i < 3; i++ {
		f.app.freezeImageGenerationTurn(f.entry)
	}
	if snap := f.state.snapshot(); !snap.Available || f.bridge.posts.Load() != before {
		t.Fatalf("freeze depended on the network: %+v", snap)
	}
}

var _ imagegentool.Host = imageGenerationHost{}
