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
	if cfg.BridgeBaseURL != "https://bridge-aibase.ouc-online.com.cn" {
		t.Fatalf("bridge = %q", cfg.BridgeBaseURL)
	}
	if cfg.RedirectURI != "https://bridge-aibase.ouc-online.com.cn/oauth/callback" {
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
	models, err := app.PassportModels("")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "qwen-max" || models[1].OwnedBy != "zhipu" {
		t.Fatalf("models = %+v", models)
	}
}

func TestPassportModelsForTenant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/t/changsha/v1/models" {
			t.Fatalf("path = %s, want tenant-scoped", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-4.6","owned_by":"zhipu"}]}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)
	t.Setenv("APPDATA", t.TempDir())

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	models, err := app.PassportModels("changsha")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "glm-4.6" {
		t.Fatalf("models = %+v", models)
	}
}

func TestPassportTenants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tenants" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// parentId 必须透传给前端渲染层级树——protocol.PassportTenant 一度缺此
		// 字段导致被静默丢弃、树退化成平铺
		_, _ = w.Write([]byte(`[{"id":"guokai","name":"国开","parentId":"SuperTenant"},{"id":"changsha","name":"changs学院","parentId":"guokai"}]`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)
	t.Setenv("APPDATA", t.TempDir())

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	tenants, err := app.PassportTenants()
	if err != nil {
		t.Fatal(err)
	}
	if len(tenants) != 2 || tenants[0].ID != "guokai" || tenants[1].Name != "changs学院" {
		t.Fatalf("tenants = %+v", tenants)
	}
	if tenants[0].ParentID != "SuperTenant" || tenants[1].ParentID != "guokai" {
		t.Fatalf("parentId 未透传: %+v", tenants)
	}
}

func TestApplyPassportTenantScopedBaseURL(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "http://bridge.local:8199")
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	req := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "glm-4.6", TenantID: "changsha"}
	cfg, err := buildConfig(req)
	if err != nil {
		t.Fatal(err)
	}
	cfg = app.applyPassport(cfg, req)
	if cfg.BaseURL != "http://bridge.local:8199/t/changsha/v1" {
		t.Fatalf("baseURL = %q, want tenant-scoped", cfg.BaseURL)
	}
}

func TestSetActiveTenantPersistsAndRestores(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})

	if err := app.SetActiveTenant(" child "); err != nil {
		t.Fatal(err)
	}
	if got := app.ActiveTenant(); got != "child" {
		t.Fatalf("active tenant = %q, want child", got)
	}
	if got := loadRawConfig().TenantID; got != "child" {
		t.Fatalf("persisted tenant = %q, want child", got)
	}
	if got := New(&recordingSink{}).ActiveTenant(); got != "child" {
		t.Fatalf("restored tenant = %q, want child", got)
	}

	if err := app.SetActiveTenant("   "); err != nil {
		t.Fatal(err)
	}
	if got := app.ActiveTenant(); got != "" {
		t.Fatalf("active tenant after clear = %q", got)
	}
	if got := loadRawConfig().TenantID; got != "" {
		t.Fatalf("persisted tenant after clear = %q", got)
	}
}

func TestNewRestoresPersistedActiveTenant(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if err := updateRawConfig(func(raw *StartSessionRequest) error {
		raw.TenantID = " persisted "
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := New(&recordingSink{}).ActiveTenant(); got != "persisted" {
		t.Fatalf("restored tenant = %q, want persisted", got)
	}
}

func TestSetActiveTenantUpdatesBridgeBaseURL(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "http://bridge.local:8199")
	app := New(&recordingSink{})
	app.mu.Lock()
	app.config.Provider = "openai"
	app.config.BaseURL = "http://bridge.local:8199/v1"
	app.configPassport = true
	app.mu.Unlock()

	if err := app.SetActiveTenant("changsha"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	baseURL := app.config.BaseURL
	app.mu.Unlock()
	if baseURL != "http://bridge.local:8199/t/changsha/v1" {
		t.Fatalf("baseURL = %q, want tenant-scoped bridge URL", baseURL)
	}
}

func TestSetActiveTenantLeavesCustomBridgeLikeURLUnchanged(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", "https://bridge.example")
	app := New(&recordingSink{})
	app.mu.Lock()
	app.config.Provider = "openai"
	app.config.BaseURL = "https://bridge.example.custom/v1"
	app.configPassport = false
	app.mu.Unlock()

	if err := app.SetActiveTenant("tenant-b"); err != nil {
		t.Fatal(err)
	}
	app.mu.Lock()
	baseURL := app.config.BaseURL
	app.mu.Unlock()
	if baseURL != "https://bridge.example.custom/v1" {
		t.Fatalf("custom baseURL rewritten to %q", baseURL)
	}
}

func TestSessionModelsKeepLiveTenantAfterNextTenantChanges(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})
	app.mu.Lock()
	app.currentID = "live-session"
	app.livePassport = true
	app.livePassportTenant = "tenant-a"
	app.passportTenant = "tenant-b"
	app.mu.Unlock()

	if _, err := app.SessionModels(); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/t/tenant-a/v1/models" {
		t.Fatalf("models path = %q, want live tenant A", gotPath)
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
	liveCfg := app.liveConfig
	configPassport := app.configPassport
	livePassport := app.livePassport
	app.mu.Unlock()
	if cfg.Provider != "openai" || cfg.TokenSource == nil || !configPassport {
		t.Fatalf("config after SaveSettings: provider=%q tokenSource-nil=%v passport=%v", cfg.Provider, cfg.TokenSource == nil, configPassport)
	}
	if liveCfg.Provider != "openai" || liveCfg.TokenSource == nil || !livePassport {
		t.Fatalf("live config after SaveSettings: provider=%q tokenSource-nil=%v passport=%v", liveCfg.Provider, liveCfg.TokenSource == nil, livePassport)
	}
}

func TestSaveSettingsForNextTenantDoesNotChangeLiveModel(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "AT", Expiry: time.Now().Add(time.Hour)})

	start := StartSessionRequest{CWD: t.TempDir(), Provider: "passport", Model: "model-a", TenantID: "tenant-a"}
	if _, err := app.StartSession(start); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := app.SetActiveTenant("tenant-b"); err != nil {
		t.Fatalf("SetActiveTenant: %v", err)
	}
	next := start
	next.Model = "model-b"
	next.TenantID = "tenant-b"
	if _, err := app.SaveSettings(next); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	app.mu.Lock()
	nextModel := app.config.Model
	liveModel := app.liveConfig.Model
	liveTenant := app.livePassportTenant
	app.mu.Unlock()
	if nextModel != "model-b" {
		t.Fatalf("next model = %q, want model-b", nextModel)
	}
	if liveModel != "model-a" || liveTenant != "tenant-a" {
		t.Fatalf("live connection changed: model=%q tenant=%q", liveModel, liveTenant)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
