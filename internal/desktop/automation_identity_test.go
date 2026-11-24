package desktop

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/wt68/runcode/internal/codexauth"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

//nolint:unparam // issuer remains a test seam for identity variants.
func testJWT(issuer, subject, extra string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]string{"iss": issuer, "sub": subject, "jti": extra})
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func newIdentityApp(t *testing.T) *App {
	t.Helper()
	isolateConfigDir(t)
	return newWithBuild(&recordingSink{}, func(engine.Config, engine.Options) (host.Session, error) {
		return newFakeHostSession(), nil
	})
}

func TestAutomationBindingAuthMatrix(t *testing.T) {
	app := newIdentityApp(t)
	const iss = "https://passport.example"
	app.tokens.setInMemory(tokenSet{AccessToken: testJWT(iss, "user-a", "1"), RefreshToken: "r", Expiry: time.Now().Add(time.Hour)})
	app.mu.Lock()
	app.passportTenant = "T1"
	app.mu.Unlock()
	platform, err := app.freezeAutomationBinding(automationKindPlatform, "model-a", "safe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "super-secret-key-xyz",
	}); err != nil {
		t.Fatal(err)
	}
	static, err := app.freezeAutomationBinding(automationKindCustom, "auto-static", "safe")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(static)
	if strings.Contains(string(raw), "super-secret-key-xyz") {
		t.Fatalf("binding leaked the API key: %s", raw)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "codex-a", Provider: "codex", Model: "gpt-test", AuthMode: "chatgpt",
	}); err != nil {
		t.Fatal(err)
	}
	app.codex.Set(codexauth.Tokens{
		AccessToken: "access-a", RefreshToken: "refresh-a", Expiry: time.Now().Add(time.Hour),
		AccountID: "acct-a", Email: "a@example.com", Subject: "user-a", Issuer: "https://auth.openai.com",
	})
	codexBinding, err := app.freezeAutomationBinding(automationKindCodex, "codex-a", "safe")
	if err != nil {
		t.Fatal(err)
	}

	app.tokens.setInMemory(tokenSet{AccessToken: testJWT(iss, "user-b", "1"), RefreshToken: "r2", Expiry: time.Now().Add(time.Hour)})
	app.mu.Lock()
	app.passportTenant = "T2"
	app.mu.Unlock()
	if err := app.validateAutomationBinding(platform); err == nil || !strings.Contains(err.Error(), "账号或密钥") {
		t.Fatalf("platform switch = %v", err)
	}
	if err := app.validateAutomationBinding(static); err != nil {
		t.Fatalf("static binding followed the passport switch: %v", err)
	}
	app.codex.Set(codexauth.Tokens{
		AccessToken: "access-b", RefreshToken: "refresh-b", Expiry: time.Now().Add(time.Hour),
		AccountID: "acct-b", Email: "b@example.com", Subject: "user-b", Issuer: "https://auth.openai.com",
	})
	if err := app.validateAutomationBinding(codexBinding); err == nil || !strings.Contains(err.Error(), "账号或密钥") {
		t.Fatalf("codex switch = %v", err)
	}

	app.codex.Set(codexauth.Tokens{
		AccessToken: "access-a", RefreshToken: "refresh-a", Expiry: time.Now().Add(time.Hour),
		AccountID: "acct-a", Email: "a@example.com",
	})
	if _, err := app.freezeAutomationBinding(automationKindCodex, "codex-a", "safe"); err == nil || !strings.Contains(err.Error(), "重新登录") {
		t.Fatalf("missing subject = %v", err)
	}

	firstDigest := static.ConnectionDigest
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "auto-static", Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "next-secret-key",
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := app.freezeAutomationBinding(automationKindCustom, "auto-static", "safe")
	if err != nil {
		t.Fatal(err)
	}
	if changed.ConnectionDigest != firstDigest {
		t.Fatal("key change rewrote the connection digest")
	}
	if changed.CredentialRevision == static.CredentialRevision {
		t.Fatal("key change did not bump the credential revision")
	}
	if err := app.validateAutomationBinding(static); err == nil {
		t.Fatal("old binding still validated after the key change")
	}

	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "auto-static", Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "legacy-key",
	}); err != nil {
		t.Fatal(err)
	}
	if err := updateRawConfig(func(cfg *desktopConfig) error {
		for i := range cfg.CustomModels {
			if cfg.CustomModels[i].Name == "auto-static" {
				cfg.CustomModels[i].CredentialRevision = 0
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := app.freezeAutomationBinding(automationKindCustom, "auto-static", "safe")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.CredentialRevision != 1 {
		t.Fatalf("legacy revision = %d, want 1", legacy.CredentialRevision)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "auto-static", Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "legacy-key",
	}); err != nil {
		t.Fatal(err)
	}
	rev := int64(-1)
	for _, m := range loadRawConfig().CustomModels {
		if m.Name == "auto-static" {
			rev = m.CredentialRevision
		}
	}
	if rev != 1 {
		t.Fatalf("saving the same key persisted revision %d", rev)
	}
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		OriginalName: "auto-static", Name: "auto-static", Provider: "openai", Model: "static-model",
		BaseURL: "http://127.0.0.1:9", APIKey: "legacy-key-2",
	}); err != nil {
		t.Fatal(err)
	}
	rev = 0
	for _, m := range loadRawConfig().CustomModels {
		if m.Name == "auto-static" {
			rev = m.CredentialRevision
		}
	}
	if rev != 2 {
		t.Fatalf("revision after a real change = %d, want 2", rev)
	}
}

func TestAutomationCredentialSwitchBetweenCheckAndRead(t *testing.T) {
	app := newIdentityApp(t)
	if _, err := app.SaveCustomModel(SaveCustomModelRequest{
		Name: "codex-a", Provider: "codex", Model: "gpt-test", AuthMode: "chatgpt",
	}); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	app.codex.Set(codexauth.Tokens{
		AccessToken: "access-a", RefreshToken: "refresh-a", Expiry: future,
		AccountID: "acct-a", Email: "a@example.com", Subject: "user-a", Issuer: "https://auth.openai.com",
	})
	app.setCodexExpect("codex-a", codexIdentityExpect{Subject: "user-a", AccountID: "acct-a"})
	up, ok := app.resolveCodexUpstream("codex-a")
	if !ok {
		t.Fatal("codex profile did not resolve")
	}
	t.Cleanup(func() { automationCredentialGap = nil })
	automationCredentialGap = func() {
		app.codex.Set(codexauth.Tokens{
			AccessToken: "access-b", RefreshToken: "refresh-b", Expiry: future,
			AccountID: "acct-b", Email: "b@example.com", Subject: "user-b", Issuer: "https://auth.openai.com",
		})
	}
	tok, err := up.Bearer()
	if err == nil || tok == "access-b" || strings.Contains(tok, "access-b") {
		t.Fatalf("switched account returned %q, %v", tok, err)
	}

	app.codex.Set(codexauth.Tokens{
		AccessToken: "access-a", RefreshToken: "refresh-a", Expiry: future,
		AccountID: "acct-a", Email: "a@example.com", Subject: "user-a", Issuer: "https://auth.openai.com",
	})
	app.setCodexExpect("codex-a", codexIdentityExpect{Subject: "user-a", AccountID: "acct-a"})
	up, _ = app.resolveCodexUpstream("codex-a")
	automationCredentialGap = func() { app.codex.noteRefresh("access-a2") }
	tok, err = up.Bearer()
	if err != nil || tok != "access-a2" {
		t.Fatalf("same-account refresh = %q, %v", tok, err)
	}

	const iss = "https://passport.example"
	app.tokens.setInMemory(tokenSet{AccessToken: testJWT(iss, "user-a", "1"), RefreshToken: "r", Expiry: future})
	automationCredentialGap = func() {
		app.tokens.Set(tokenSet{AccessToken: testJWT(iss, "user-b", "1"), RefreshToken: "r2", Expiry: future})
	}
	tok, err = app.tokens.TokenFor(iss, "user-a")
	if err == nil || tok != "" {
		t.Fatalf("passport switch returned %q, %v", tok, err)
	}
	app.tokens.setInMemory(tokenSet{AccessToken: testJWT(iss, "user-a", "1"), RefreshToken: "r", Expiry: future})
	refreshed := testJWT(iss, "user-a", "2")
	automationCredentialGap = func() {
		app.tokens.noteRefresh(tokenSet{AccessToken: refreshed, RefreshToken: "r", Expiry: future})
	}
	tok, err = app.tokens.TokenFor(iss, "user-a")
	if err != nil || tok != refreshed {
		t.Fatalf("passport refresh = %q, %v", tok, err)
	}

	done := make(chan struct{})
	app.tokens.onLoggedOut = func() {
		app.mu.Lock()
		runtime.Gosched()
		app.mu.Unlock()
		app.tokens.mu.Lock()
		runtime.Gosched()
		app.tokens.mu.Unlock()
		close(done)
	}
	app.tokens.tokenURL = "http://127.0.0.1:1/token"
	app.tokens.setInMemory(tokenSet{AccessToken: testJWT(iss, "user-a", "1"), RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)})
	go func() { _, _ = app.tokens.Token() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("logout callback did not return; token lock is held across the callback")
	}
}
