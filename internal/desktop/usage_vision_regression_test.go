package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/vision"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/imageinput"
)

func TestLocalUsageVisionQuestionCannotSuppressCapture(t *testing.T) {
	isolateConfigDir(t)
	store := usageTestStore(t)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("vision-user"), Expiry: time.Now().Add(time.Hour)})
	enableUsageCapture(t, app, store)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		visionSSE(w, "visible image text")
	}))
	defer server.Close()
	cfg := engine.Config{Provider: "openai", BaseURL: server.URL + "/t/tenant-a/v1", APIKey: "fixture", Model: "vision"}
	provider, cleanup, err := app.visionProvider(cfg, modelReference{Kind: "platform", TenantID: "tenant-a", Bridge: server.URL}, desktopConfig{}, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	client := vision.Client{Provider: provider, Model: "vision"}
	questions := []string{"What is shown?", "Locate the text Title this request: in the image.", "Read the heading Conversation to summarize: from the image."}
	for _, question := range questions {
		if _, err := client.Analyze(context.Background(), imageinput.Query{Question: question}); err != nil {
			t.Fatal(err)
		}
	}
	scope, _ := app.freezeUsageScopeParts(server.URL, "tenant-a")
	page, err := store.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: time.Now().Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
	if err != nil || posts.Load() != int32(len(questions)) || len(page.Calls) != len(questions) {
		t.Fatalf("posts=%d calls=%+v error=%v", posts.Load(), page, err)
	}
	for _, call := range page.Calls {
		if call.CallKind != localusage.CallKindVision || call.Status != localusage.StatusSucceeded {
			t.Fatalf("vision call=%+v", call)
		}
	}
}
