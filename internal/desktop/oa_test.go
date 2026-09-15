package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"

	"github.com/wt68/runcode/internal/oatool"
)

// primeLocalModel 把某租户的本地模型解析结果直接写进缓存，让测试不必联网。
func primeLocalModel(app *App, tenant string, entry localModelEntry) {
	app.mu.Lock()
	if app.localModels == nil {
		app.localModels = map[string]localModelEntry{}
	}
	entry.at = time.Now()
	app.localModels[tenant] = entry
	app.mu.Unlock()
}

// primeOABound 预置 OA 绑定探测结果。注册判据有三条，测某一条时另外两条必须先满足，
// 否则用例会因为**别的**原因通过——那样它什么也没证明。
func primeOABound(app *App, tenant string, bound bool) {
	app.mu.Lock()
	if app.oaBindings == nil {
		app.oaBindings = map[string]oaBindingEntry{}
	}
	app.oaBindings[tenant] = oaBindingEntry{bound: bound, at: time.Now()}
	app.mu.Unlock()
}

// primeOAReady 一次满足"有本地模型 + 已绑 OA"两条，留下待测的那一条。
func primeOAReady(app *App, tenant string) {
	primeLocalModel(app, tenant, localModelEntry{model: PassportModel{ID: "qwen3.6-27b", Local: true}, found: true})
	primeOABound(app, tenant, true)
}

// OA 端点由会话的 Bridge 基地址推出，选定的租户自动跟着——OA 用量与对话记在同一个
// 租户名下。与联网搜索同一套推导。
func TestOAEndpointKeepsTenantPrefix(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://bridge.example/v1", "https://bridge.example/v1/oa"},
		{"https://bridge.example/t/t-42/v1", "https://bridge.example/t/t-42/v1/oa"},
		{"https://bridge.example/v1/", "https://bridge.example/v1/oa"},
		{"   ", ""},
	} {
		if got := oaEndpoint(tc.base); got != tc.want {
			t.Errorf("oaEndpoint(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestPassportTenantOf(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://bridge.example/t/t-42/v1", "t-42"},
		{"https://bridge.example/t/changsha/v1/", "changsha"},
		{"https://bridge.example/v1", ""},
		{"", ""},
	} {
		if got := passportTenantOf(tc.base); got != tc.want {
			t.Errorf("passportTenantOf(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// resolveLocalModel 的三条规则。第三条(多个本地模型、一个都没标默认 → 判定为没有)
// 是有意失败关闭:"随便挑一个"会让保密数据流向一个没人指定过的模型。
func TestResolveLocalModel(t *testing.T) {
	cloud := PassportModel{ID: "glm-5.1"}
	localA := PassportModel{ID: "qwen3.6-27b", Local: true}
	localB := PassportModel{ID: "qwen3.6-8b", Local: true}
	defaultB := PassportModel{ID: "qwen3.6-8b", Local: true, LocalDefault: true}

	cases := []struct {
		name   string
		models []PassportModel
		want   string
	}{
		{"显式默认胜出", []PassportModel{cloud, localA, defaultB}, "qwen3.6-8b"},
		{"只有一个本地模型就用它", []PassportModel{cloud, localA}, "qwen3.6-27b"},
		{"没有本地模型", []PassportModel{cloud}, ""},
		{"空清单", nil, ""},
		{"多个本地却无默认 → 失败关闭", []PassportModel{cloud, localA, localB}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveLocalModel(tc.models)
			if tc.want == "" {
				if ok {
					t.Fatalf("resolved %q, want none", got.ID)
				}
				return
			}
			if !ok {
				t.Fatal("no local model resolved")
			}
			if got.ID != tc.want {
				t.Fatalf("resolved %q, want %q", got.ID, tc.want)
			}
		})
	}
}

// 安全红线:自填端点/自定义模型那条路一定不装 OA 工具。那条连接指向第三方,
// 既不该收到用户令牌,更不该产出 OA 数据。判据是 TokenSource,与联网搜索一致。
func TestOAToolsRefuseDirectConnection(t *testing.T) {
	app := New(&recordingSink{})
	// 就算另外两条判据都满足，直连也一条都不给。
	primeOAReady(app, "")

	direct := &engine.Config{BaseURL: "https://api.example/v1", APIKey: "sk-x"}
	if got := app.oaTools(direct, "sess"); got != nil {
		t.Fatal("a direct connection was given OA tools")
	}
	if got := app.oaTools(nil, "sess"); got != nil {
		t.Fatal("a nil config produced OA tools")
	}
}

// 没有可用本地模型就一条都不装。装上只会让模型反复去试、每次撞闸门,还白占上下文。
// 这也是"租户没开通本地模型时绝不退回云端"那条的落点——不装,而不是装上再退让。
func TestOAToolsRequireLocalModel(t *testing.T) {
	app := New(&recordingSink{})
	primeOABound(app, "t-1", true) // 绑定没问题，缺的只是本地模型
	primeLocalModel(app, "t-1", localModelEntry{found: false})

	cfg := &engine.Config{
		BaseURL:     "https://bridge.example/t/t-1/v1",
		TokenSource: func() (string, error) { return "AT", nil },
	}
	if got := app.oaTools(cfg, "sess"); got != nil {
		t.Fatalf("tenant without a local model got %d OA tools", len(got))
	}
}

// 条件齐备时装齐全套，且每一条都带着闸门。
func TestOAToolsBuiltForPassportWithLocalModel(t *testing.T) {
	app := New(&recordingSink{})
	primeOAReady(app, "t-1")

	cfg := &engine.Config{
		BaseURL:     "https://bridge.example/t/t-1/v1",
		TokenSource: func() (string, error) { return "AT", nil },
	}
	tools := app.oaTools(cfg, "sess")
	if len(tools) != len(oatool.Names()) {
		t.Fatalf("built %d OA tools, want %d", len(tools), len(oatool.Names()))
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name(), "oa_") {
			t.Errorf("tool %q lacks the oa_ prefix", tl.Name())
		}
	}
}

// 没绑 OA 的账号不装:它调任何一条都只会拿到同一句"请联系管理员绑定工号",
// 带着 15 条注定失败的工具走完全程没有意义,还白占上下文。
func TestOAToolsRequireBinding(t *testing.T) {
	app := New(&recordingSink{})
	primeLocalModel(app, "t-1", localModelEntry{model: PassportModel{ID: "qwen3.6-27b", Local: true}, found: true})
	primeOABound(app, "t-1", false)

	cfg := &engine.Config{
		BaseURL:     "https://bridge.example/t/t-1/v1",
		TokenSource: func() (string, error) { return "AT", nil },
	}
	if got := app.oaTools(cfg, "sess"); got != nil {
		t.Fatalf("an account without an OA binding got %d OA tools", len(got))
	}
}

// 探测失败与"确实没绑"必须用不同的缓存时长。
//
// 真实事故：/v1/oa/status 的实测耗时是 2.07s / 1.52s / 0.24s，而超时原先设成 2s，
// 于是时灵时不灵；更糟的是失败结果被缓存 10 分钟，一次网络抖动会让 OA 工具消失
// 十分钟，而用户只会看到工具凭空不见、没有任何提示。
func TestOABindingFailureIsCachedBriefly(t *testing.T) {
	fail := oaBindingEntry{bound: false, failed: true}
	miss := oaBindingEntry{bound: false, failed: false}
	if fail.ttl() >= miss.ttl() {
		t.Fatalf("失败的缓存时长 %v 不该大于等于确定结果的 %v", fail.ttl(), miss.ttl())
	}
	if fail.ttl() > time.Minute {
		t.Errorf("失败缓存 %v 太长：网络抖一下不该让 OA 工具消失这么久", fail.ttl())
	}
}

// 两条探测的超时都必须宽于真实链路耗时。OA 那条更长（客户端 → Bridge → OA REST
// → Passport.API → OA 人员库），所以不能比模型清单那条还短。
func TestOAProbeTimeoutsLeaveHeadroom(t *testing.T) {
	if localModelColdTimeout < 5*time.Second {
		t.Errorf("模型清单探测超时 %v 太紧；实测公网 Bridge 冷启动要 2s 以上", localModelColdTimeout)
	}
	if oaStatusTimeout < localModelColdTimeout {
		t.Errorf("OA 绑定探测 %v 不该短于模型清单 %v——它的链路更长",
			oaStatusTimeout, localModelColdTimeout)
	}
}

// 权限归类漏一条的表现是"那个工具一调就被拒":引擎的解析器不认识的名字会解析成
// unknown/高风险,默认策略对它硬拒。所以这张表必须覆盖全部 OA 工具。
func TestHostToolClassesCoverEveryOATool(t *testing.T) {
	for _, name := range oatool.Names() {
		class, ok := hostToolClasses[name]
		if !ok {
			t.Errorf("tool %q has no permission class; it would be hard-denied", name)
			continue
		}
		if class != permissions.ClassReadOnly {
			t.Errorf("tool %q class = %v, want ClassReadOnly", name, class)
		}
	}
}

// 闸门在会话查不到时必须拒绝。失败关闭是这道闸唯一可接受的默认——查不到就放行
// 等于在会话重建的窗口里开了一扇门。
func TestOAGateDeniesWhenSessionUnknown(t *testing.T) {
	app := New(&recordingSink{})
	err := app.oaGate("no-such-session", "qwen3.6-27b")(context.Background(), "oa_todo", "tu_1")
	if err == nil {
		t.Fatal("gate opened for an unknown session")
	}
	if !strings.Contains(err.Error(), "qwen3.6-27b") {
		t.Fatalf("gate message should name the local model, got %q", err)
	}
}

// 闸门的措辞要同时做到三件事:说清是规则不是故障、让模型别自己编、给出下一步。
// 少一件都会变成一次糟糕的对话(模型拿常识胡诌一份待办是真实发生过的失败模式)。
func TestOAGateErrorTellsModelNotToGuess(t *testing.T) {
	msg := oaGateError("glm-5.1", "qwen3.6-27b").Error()
	for _, want := range []string{"glm-5.1", "qwen3.6-27b", "保密", "不要凭猜测"} {
		if !strings.Contains(msg, want) {
			t.Errorf("gate message missing %q: %s", want, msg)
		}
	}
}

// configureSession 这一端的接线:直连不装、通行证+本地模型才装。
// 顺带钉住"OA 工具走 ExtraTools" —— 引擎在加 Task 之前就把子代理可用工具快照走了,
// 所以这条路径本身就是"子代理拿不到 OA 工具"的保证。
func TestConfigureSessionInstallsOAOnlyWithLocalModel(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	sctx := host.SessionContext{
		ID:       "sess_oa",
		Approver: host.NewAsyncApprover(func(string, any) {}, ws),
		Emit:     func(string, any) {},
	}
	countOA := func(opts engine.Options) int {
		n := 0
		for _, tl := range opts.ExtraTools {
			if strings.HasPrefix(tl.Name(), "oa_") {
				n++
			}
		}
		return n
	}

	direct := engine.Config{CWD: ws, PermissionMode: "interactive", BaseURL: "https://api.example/v1"}
	var opts engine.Options
	app.configureSession(sctx, &direct, &opts)
	if got := countOA(opts); got != 0 {
		t.Fatalf("a direct connection got %d OA tools", got)
	}

	primeOAReady(app, "t-1")
	passport := engine.Config{
		CWD:            ws,
		PermissionMode: "interactive",
		BaseURL:        "https://bridge.example/t/t-1/v1",
		TokenSource:    func() (string, error) { return "AT", nil },
	}
	opts = engine.Options{}
	app.configureSession(sctx, &passport, &opts)
	if got, want := countOA(opts), len(oatool.Names()); got != want {
		t.Fatalf("passport session got %d OA tools, want %d", got, want)
	}
}

// 缓存命中时不联网:会话创建是延迟敏感路径,每次开会话多一趟 Bridge 往返是不可
// 接受的。这条同时保证了整个测试套件不会因为 OA 而去碰网络。
func TestLocalModelServedFromCache(t *testing.T) {
	app := New(&recordingSink{})
	want := PassportModel{ID: "qwen3.6-27b", Local: true, ContextTokens: 65536}
	primeLocalModel(app, "t-1", localModelEntry{model: want, found: true})

	got, ok := app.localModel("t-1")
	if !ok {
		t.Fatal("cached local model not found")
	}
	if got.ID != want.ID || got.ContextTokens != want.ContextTokens {
		t.Fatalf("localModel = %+v, want %+v", got, want)
	}
}
