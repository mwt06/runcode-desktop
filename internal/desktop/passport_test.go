package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPassportConfigDefaultsAndEnvOverride(t *testing.T) {
	t.Setenv("RUNCODE_PASSPORT_AUTHORITY", "")
	t.Setenv("RUNCODE_PASSPORT_CLIENT_ID", "")
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "")
	cfg := passportConfig()
	if cfg.Authority != "https://passport-ai.ouchn.edu.cn" {
		t.Fatalf("authority = %q", cfg.Authority)
	}
	if cfg.ClientID != "runcode-desktop" {
		t.Fatalf("clientID = %q", cfg.ClientID)
	}
	if cfg.BridgeBaseURL != "http://localhost:8199" {
		t.Fatalf("bridge = %q", cfg.BridgeBaseURL)
	}
	if cfg.RedirectURI != "http://localhost:8199/oauth/callback" {
		t.Fatalf("redirect = %q", cfg.RedirectURI)
	}

	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://bridge.example/")
	cfg = passportConfig()
	if cfg.BridgeBaseURL != "https://bridge.example" || cfg.RedirectURI != "https://bridge.example/oauth/callback" {
		t.Fatalf("override: %+v", cfg)
	}
}

func TestApplyPassportProvider(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离 passport.json，New() 的 loadPersisted 不读真实登录
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	req := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "qwen-max"}
	cfg, err := buildConfig(req)
	if err != nil {
		t.Fatal(err)
	}
	cfg = app.applyPassport(cfg, req)
	if cfg.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", cfg.Provider)
	}
	if cfg.BaseURL != passportConfig().BridgeBaseURL+"/v1" {
		t.Fatalf("baseURL = %q", cfg.BaseURL)
	}
	if cfg.TokenSource == nil {
		t.Fatal("TokenSource must be wired")
	}
	if tok, err := cfg.TokenSource(); err != nil || tok != "AT" {
		t.Fatalf("TokenSource() = %q, %v", tok, err)
	}
}

func TestPassportModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer AT" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-max","owned_by":"qwen"},{"id":"glm-4","owned_by":"zhipu"}]}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)
	t.Setenv("APPDATA", t.TempDir()) // 隔离 passport.json

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	models, err := app.PassportModels()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "qwen-max" || models[1].OwnedBy != "zhipu" {
		t.Fatalf("models = %+v", models)
	}
}

func TestPassportLogoutClearsAndEmits(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // 隔离：PassportLogout 会删除 passport.json，不能碰真实登录
	sink := &recordingSink{}
	app := New(sink)
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	app.PassportLogout()
	if app.tokens.LoggedIn() {
		t.Fatal("tokens must be cleared")
	}
	if _, ok := sink.lastOf(EventPassportChanged); !ok {
		t.Fatal("passport:changed must be emitted")
	}
}

func TestSaveSettingsKeepsPassportWiring(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	req := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "qwen-max"}
	if _, err := app.StartSession(req); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := app.SaveSettings(req); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	app.mu.Lock()
	cfg := app.config
	app.mu.Unlock()
	if cfg.Provider != "openai" || cfg.TokenSource == nil {
		t.Fatalf("config after SaveSettings: provider=%q tokenSource-nil=%v, want openai wiring kept", cfg.Provider, cfg.TokenSource == nil)
	}
}

func TestStartSessionPassportRequiresLogin(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	req := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "qwen-max"}
	if _, err := app.StartSession(req); err == nil {
		t.Fatal("want error when starting passport session while logged out")
	}
}

func TestPassportStatusRetriesFetchMeAfterFailure(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	fail := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"userId":"u-1","userName":"zhangsan","name":"张三","tenantId":"t-1"}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	st := app.PassportStatus()
	if !st.LoggedIn || st.UserID != "" {
		t.Fatalf("first status = %+v, want logged-in placeholder without profile", st)
	}

	fail = false
	st = app.PassportStatus()
	if st.UserID != "u-1" || st.Name != "张三" {
		t.Fatalf("second status = %+v, want full profile after bridge recovers (fetchMe must be retried)", st)
	}
}
