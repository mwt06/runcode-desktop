package desktop

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/localusage"
)

func TestPlatformUsageMissingIdentityPersistsSafeCoverage(t *testing.T) {
	isolateConfigDir(t)
	store := usageTestStore(t)
	identity := func(context.Context) (ScopeFreeze, error) {
		return ScopeFreeze{}, fmt.Errorf("opaque-secret-token https://private.example")
	}
	setPlatformCapture(store, identity)
	t.Cleanup(func() { setPlatformCapture(nil, nil) })
	freeze := platformUsageFreezer("https://private.example/t/tenant-a/v1", identity)
	for i := 0; i < 3; i++ {
		if _, err := freeze(context.Background()); err == nil {
			t.Fatal("missing identity was attributed to a scope")
		}
	}
	var count int
	if err := localUsageTestDB(t, store).QueryRow(`SELECT COUNT(*) FROM diagnostics`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("coverage failures must persist one bounded diagnostic, got %d", count)
	}
	var code, detail string
	if err := localUsageTestDB(t, store).QueryRow(`SELECT code, detail FROM diagnostics`).Scan(&code, &detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code+detail, "missing_stable_identity") || strings.Contains(code+detail, "private.example") || strings.Contains(code+detail, "secret-token") {
		t.Fatalf("diagnostic was absent or unsafe: %q %q", code, detail)
	}
	if err := localUsageTestDB(t, store).QueryRow(`SELECT COUNT(*) FROM scopes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unknown identity created %d statistical scopes, err=%v", count, err)
	}
	other := ScopeFreeze{AccountIssuer: "issuer", AccountSub: "account", BridgeBase: "https://private.example", TenantID: "tenant-b"}.toScope()
	coverage, err := store.QueryCoverage(context.Background(), other, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil || len(coverage.Gaps) != 0 {
		t.Fatalf("known tenant-a failure leaked into tenant-b coverage: %+v err=%v", coverage, err)
	}
}

func TestNonLLMScopeFailurePersistsCoverage(t *testing.T) {
	for _, tc := range []struct{ name, token, tenant, reason string }{
		{"opaque", "opaque-token", "tenant-a", "missing_stable_identity"},
		{"tenant", imageGenJWT("user"), "", "invalid_platform_scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigDir(t)
			store := usageTestStore(t)
			app := New(&recordingSink{})
			app.tokens.setInMemory(tokenSet{AccessToken: tc.token, Expiry: time.Now().Add(time.Hour)})
			enableUsageCapture(t, app, store)
			if _, err := app.freezeUsageScopeParts("https://bridge.example", tc.tenant); err == nil {
				t.Fatal("invalid non-LLM identity accepted")
			}
			var detail string
			if err := localUsageTestDB(t, store).QueryRow(`SELECT detail FROM diagnostics`).Scan(&detail); err != nil || !strings.Contains(detail, tc.reason) {
				t.Fatalf("non-LLM scope failure disappeared: detail=%q err=%v", detail, err)
			}
		})
	}
}

func TestPlatformUsageInvalidTenantPersistsKnownAccountGap(t *testing.T) {
	isolateConfigDir(t)
	store := usageTestStore(t)
	identity := func(context.Context) (ScopeFreeze, error) {
		return ScopeFreeze{AccountIssuer: "issuer", AccountSub: "account-a"}, nil
	}
	setPlatformCapture(store, identity)
	t.Cleanup(func() { setPlatformCapture(nil, nil) })
	freeze := platformUsageFreezer("https://private.example/v1", identity)
	if _, err := freeze(context.Background()); err == nil {
		t.Fatal("missing tenant was attributed to a scope")
	}
	var detail string
	if err := localUsageTestDB(t, store).QueryRow(`SELECT detail FROM diagnostics`).Scan(&detail); err != nil {
		t.Fatalf("missing tenant did not persist coverage: %v", err)
	}
	if !strings.Contains(detail, digestPair("issuer", "account-a")) || strings.Contains(detail, "private.example") {
		t.Fatalf("known account was not retained safely: %q", detail)
	}
}

func TestLocalUsageDisabledStillOpensHistory(t *testing.T) {
	isolateConfigDir(t)
	store := usageTestStore(t)
	at := time.Now().UTC()
	scope := ScopeFreeze{AccountIssuer: "issuer", AccountSub: "account-a", BridgeBase: "https://bridge.example", TenantID: "tenant-a"}.toScope()
	if err := store.Begin(context.Background(), localusage.StartRecord{CallID: "historical", Scope: scope, Model: "model", CallKind: localusage.CallKindLLM, StartedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envLocalUsageEnabled, "false")
	app := New(&recordingSink{})
	if err := app.initLocalUsage(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.closeLocalUsage)
	if app.usageStore == nil {
		t.Fatal("disabling collection hid historical usage")
	}
	if platformEnabled.Load() || currentPlatformRecorder() != nil {
		t.Fatal("disabled collection still accepts writes")
	}
	got, err := app.usageStore.QueryPage(context.Background(), localusage.QueryFilter{ScopeID: scope.ScopeID, StartAt: at.Add(-time.Hour), EndAt: at.Add(time.Hour)}, "", 1000)
	if err != nil || len(got.Calls) != 1 {
		t.Fatalf("history unavailable: %+v err=%v", got, err)
	}
}
