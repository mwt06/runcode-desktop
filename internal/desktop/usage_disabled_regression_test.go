package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
	"github.com/wt68/runcode/internal/protocol"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
)

func TestLocalUsageDisabledIntervalSurvivesThreeStarts(t *testing.T) {
	isolateConfigDir(t)
	t.Setenv(envLocalUsageEnabled, "true")
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("disabled-user"), Expiry: time.Now().Add(time.Hour)})
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		_, _ = w.Write([]byte(`{"State":true,"Data":{"result":[]}}`))
	}))
	defer server.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", server.URL)
	if err := app.initLocalUsage(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLocalUsage)
	scope, _ := app.freezeUsageScopeParts(server.URL, "tenant-a")
	at := time.Now().Add(-3 * time.Hour)
	start := localusage.StartRecord{CallID: "history", Scope: scope, Model: "m", CallKind: localusage.CallKindLLM, StartedAt: at}
	if err := app.usageStore.Begin(context.Background(), start); err != nil {
		t.Fatal(err)
	}
	if err := app.usageStore.Finish(context.Background(), localusage.FinishRecord{CallID: start.CallID, Status: localusage.StatusSucceeded, FinishedAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	app.closeLocalUsage()
	t.Setenv(envLocalUsageEnabled, "false")
	if err := app.initLocalUsage(); err != nil {
		t.Fatal(err)
	}
	search := passportWebSearchFor(app, &engine.Config{BaseURL: server.URL + "/t/tenant-a/v1", TokenSource: func() (string, error) { return "AT", nil }})
	if result, err := search.Run(context.Background(), json.RawMessage(`{"query":"q"}`), nil, nil); err != nil || result.IsError {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	app.closeLocalUsage()
	t.Setenv(envLocalUsageEnabled, "true")
	if err := app.initLocalUsage(); err != nil {
		t.Fatal(err)
	}
	coverage, err := app.usageStore.QueryCoverage(context.Background(), scope, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || posts.Load() != 1 || coverage.CoverageStatus != "gap" || len(coverage.Gaps) != 1 || coverage.Gaps[0].Reason != "capture_disabled" {
		t.Fatalf("posts=%d coverage=%+v error=%v", posts.Load(), coverage, err)
	}
	page, err := app.usageStore.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: at.Add(-time.Hour), EndAt: time.Now().Add(time.Hour)}, "", 20)
	if err != nil || page.TotalCount != 1 {
		t.Fatalf("disabled search created usage or hid history: %+v error=%v", page, err)
	}
	for _, model := range []string{"m", "no-matches"} {
		overview, err := app.GetUsageOverview(protocol.UsageQuery{TenantID: "tenant-a", Range: "last24h", Model: model, Timezone: usageTimezone})
		if err != nil || overview.CoverageStatus != "gap" || len(overview.CoverageGaps) != 1 || overview.CoverageGaps[0].Reason != "capture_disabled" {
			t.Fatalf("model %q hid disabled coverage: %+v error=%v", model, overview, err)
		}
	}
}
