package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBindSessionIdentityDoesNotWaitForTokenLock(t *testing.T) {
	app := New(&recordingSink{})
	app.tokens.mu.Lock()
	defer app.tokens.mu.Unlock()

	e := &sessionEntry{id: "sess_cached_identity"}
	app.mu.Lock()
	defer app.mu.Unlock()

	done := make(chan struct{})
	go func() {
		bindSessionIdentity(e, sessionIdentityBinding{Account: "cached", Epoch: "epoch-1"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("binding a cached identity waited for tokenManager.mu")
	}
	if e.identity.Account != "cached" || e.identity.Epoch != "epoch-1" {
		t.Fatalf("identity = %+v", e.identity)
	}
}

func TestSessionIdentitySnapshotDoesNotAcquireAppLock(t *testing.T) {
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	app.mu.Lock()
	defer app.mu.Unlock()

	got := make(chan sessionIdentityBinding, 1)
	go func() { got <- app.sessionIdentitySnapshot() }()
	select {
	case identity := <-got:
		if identity.Account == "" || identity.Epoch == "" {
			t.Fatalf("identity snapshot = %+v", identity)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("identity snapshot waited for App.mu")
	}
}

func imageGenSessionApp(t *testing.T) (*App, string, string) {
	t.Helper()
	isolateConfigDir(t)
	fx := loadCatalogFixture(t)
	serveCatalog(t, 200, fx.Response)
	app, _, built := newParallelApp(t)
	t.Cleanup(func() { _ = app.mgr.CloseAll(context.Background()) })
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	id, sess := openParallelSession(t, app, built)
	markImageGenPassport(app, id, "tenant-a")
	return app, id, sess.cwd
}

func markImageGenPassport(app *App, id, tenant string) {
	identity := app.sessionIdentitySnapshot()
	app.mu.Lock()
	defer app.mu.Unlock()
	e := app.entryLocked(id)
	e.passport, e.tenantID = true, tenant
	bindSessionIdentity(e, identity)
}

func TestImageGenerationFreezeDoesNotReadDeadSessionSwitch(t *testing.T) {
	app, id, cwd := imageGenSessionApp(t)
	saveImageModel(t, app, "tenant-a", "image-only-b64", "")
	e, err := app.entryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	state := newImageGenerationTurnState(id, "instance-auto")
	app.mu.Lock()
	e.imageGeneration = state
	app.mu.Unlock()

	sidecar := filepath.Join(cwd, ".runcode", "image-generation", "sessions", id+".json")
	if err := os.MkdirAll(filepath.Dir(sidecar), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecar, []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatal(err)
	}

	app.freezeImageGenerationTurn(e)
	snap := state.snapshot()
	if !snap.Available || snap.Model != "image-only-b64" || snap.TurnID == "" {
		t.Fatalf("dead sidecar affected freeze: %+v", snap)
	}
}

func TestImageGenerationTurnSnapshotIsFrozenPerTurn(t *testing.T) {
	app, id, cwd := imageGenSessionApp(t)
	saveImageModel(t, app, "tenant-a", "image-only-b64", "")
	e, err := app.entryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	state := newImageGenerationTurnState(id, "instance-7")
	app.mu.Lock()
	e.imageGeneration = state
	app.mu.Unlock()

	app.freezeImageGenerationTurn(e)
	first := state.snapshot()
	if !first.Available || first.TurnID == "" || first.Model != "image-only-b64" || first.Size != "" ||
		first.Tenant != "tenant-a" || first.Workspace != cwd || first.Generation != "instance-7" || first.SettingsRevision != 1 || first.ScopeID == "" {
		t.Fatalf("frozen snapshot = %+v", first)
	}
	saveImageModel(t, app, "tenant-a", "image-only-url", "2048x2048")
	if mid := state.snapshot(); mid.Model != "image-only-b64" || mid.TurnID != first.TurnID {
		t.Fatalf("settings change leaked into the running turn: %+v", mid)
	}
	app.freezeImageGenerationTurn(e)
	next := state.snapshot()
	if next.TurnID == first.TurnID || next.Model != "image-only-url" || next.Size != "2048x2048" || next.SettingsRevision != 2 {
		t.Fatalf("next independent turn = %+v", next)
	}
}

func TestImageGenerationFreezeUnavailableWithoutModel(t *testing.T) {
	app, id, _ := imageGenSessionApp(t)
	e, err := app.entryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	state := newImageGenerationTurnState(id, "instance-nomodel")
	app.mu.Lock()
	e.imageGeneration = state
	app.mu.Unlock()

	app.freezeImageGenerationTurn(e)
	snap := state.snapshot()
	if snap.Available || !strings.Contains(snap.Reason, "设置") {
		t.Fatalf("expected settings reason without model, got %+v", snap)
	}
}

func TestImageGenerationFreezeIneligibleLoggedOutAndAccountChanged(t *testing.T) {
	app, id, _ := imageGenSessionApp(t)
	saveImageModel(t, app, "tenant-a", "image-only-b64", "")
	e, err := app.entryOf(id)
	if err != nil {
		t.Fatal(err)
	}
	state := newImageGenerationTurnState(id, "instance-identity")
	app.mu.Lock()
	e.imageGeneration = state
	e.passport = false
	app.mu.Unlock()
	app.freezeImageGenerationTurn(e)
	if snap := state.snapshot(); snap.Available || !strings.Contains(snap.Reason, "通行证") {
		t.Fatalf("non-passport freeze = %+v", snap)
	}

	markImageGenPassport(app, id, "tenant-a")
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-b"), Expiry: time.Now().Add(time.Hour)})
	app.freezeImageGenerationTurn(e)
	binding, reason := app.imageGenerationBindingOf(e)
	if reason != "" || app.imageGenerationAccountCurrent(binding) {
		t.Fatalf("account-change binding = %+v reason=%q current=%v", binding, reason, app.imageGenerationAccountCurrent(binding))
	}
}
