package desktop

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
)

func TestImageGenerationLocalRejectionsDoNotCreateUsage(t *testing.T) {
	for _, reason := range []string{"catalog withdrawn", "turn marker blocked", "ledger blocked"} {
		t.Run(reason, func(t *testing.T) {
			f := newImageGenFlow(t)
			store := usageTestStore(t)
			enableUsageCapture(t, f.app, store)
			scope, err := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
			if err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "catalog withdrawn":
				f.bridge.dropModel(t, "image-only-b64")
			default:
				dir := "turns"
				if reason == "ledger blocked" {
					dir = "requests"
				}
				path := filepath.Join(f.cwd, ".runcode", "image-generation", dir)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := f.run(t, "tu_before_do", `{"prompt":"海报"}`)
			if got.Status != "failed" || got.SubmissionStatus != "not_submitted" || f.bridge.posts.Load() != 0 {
				t.Fatalf("pre-Do rejection = %+v posts=%d", got, f.bridge.posts.Load())
			}
			if n, err := localUsageTestCountCalls(t, store, context.Background(), scope.ScopeID); err != nil || n != 0 {
				t.Fatalf("pre-Do usage count=%d err=%v", n, err)
			}
			if reason == "ledger blocked" {
				// Even if the initial ledger write fails, the persisted turn marker
				// must prevent a new tool instance from retrying the same turn.
				f.tool = f.app.newImageGenerationTool(f.state)
				again := f.run(t, "tu_new_instance", `{"prompt":"海报"}`)
				if again.Code != "turn_budget_exhausted" || f.bridge.posts.Load() != 0 {
					t.Fatalf("ledger failure released budget: %+v", again)
				}
			}
		})
	}
}

func TestImageGenerationUsageKeepsConfirmedImageCount(t *testing.T) {
	for _, status := range []string{"save_failed", "unknown"} {
		t.Run(status, func(t *testing.T) {
			f := newImageGenFlow(t)
			store := usageTestStore(t)
			enableUsageCapture(t, f.app, store)
			if status == "save_failed" {
				if err := os.WriteFile(filepath.Join(f.cwd, imageGenerationDir), []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				f.bridge.fail.Store(http.StatusBadGateway)
			}
			got := f.run(t, "tu_result", `{"prompt":"海报"}`)
			if got.Status != status || got.SubmissionStatus != "attempted" || f.bridge.posts.Load() != 1 {
				t.Fatalf("result = %+v posts=%d", got, f.bridge.posts.Load())
			}
			scope, err := f.app.freezeUsageScopeParts(imageGenerationBridge(), "tenant-a")
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Minute), EndAt: time.Now().Add(time.Minute)}, "", 1000)
			if err != nil || len(snapshot.Calls) != 1 {
				t.Fatalf("usage calls=%d err=%v", len(snapshot.Calls), err)
			}
			call := snapshot.Calls[0]
			if call.CallKind != localusage.CallKindImage || call.Status != status {
				t.Fatalf("usage result = %+v", call)
			}
			if status == "save_failed" && (call.GeneratedImages == nil || *call.GeneratedImages != 1) {
				t.Fatalf("confirmed image count lost: %+v", call)
			}
			if status == "unknown" && call.GeneratedImages != nil {
				t.Fatalf("unknown image count fabricated: %+v", call)
			}
		})
	}
}
