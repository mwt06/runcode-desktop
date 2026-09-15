package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/webfetch"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tools/websearch"
)

// —— 锁的落盘与读回 ——

// 锁必须活过进程:恢复出来的历史照样带着 OA 数据,而内存里的锁随上一个进程没了。
// 不落盘的表现是"关掉重开就能换回云端模型",且没有任何迹象。
func TestOALockSurvivesRestart(t *testing.T) {
	ws := t.TempDir()
	if _, ok := readOALock(ws, "sess_a"); ok {
		t.Fatal("a fresh session must not be locked")
	}
	if err := writeOALock(ws, "sess_a", "qwen3.6-27b"); err != nil {
		t.Fatalf("writeOALock: %v", err)
	}
	lock, ok := readOALock(ws, "sess_a")
	if !ok || lock.LocalModel != "qwen3.6-27b" {
		t.Fatalf("readOALock = %+v, %v", lock, ok)
	}
	if lock.At.IsZero() {
		t.Error("lock has no timestamp; it is the only clue when a user asks why models are frozen")
	}
	// 别的会话不受影响。
	if _, ok := readOALock(ws, "sess_b"); ok {
		t.Error("the lock leaked to another session")
	}
}

// 重复上锁不覆盖:"第一次读 OA 是什么时候"是排查线索,重写会把它抹掉。
func TestOALockIsWriteOnce(t *testing.T) {
	ws := t.TempDir()
	if err := writeOALock(ws, "s", "qwen3.6-27b"); err != nil {
		t.Fatalf("writeOALock: %v", err)
	}
	first, _ := readOALock(ws, "s")
	if err := writeOALock(ws, "s", "别的模型"); err != nil {
		t.Fatalf("writeOALock again: %v", err)
	}
	again, _ := readOALock(ws, "s")
	if again.LocalModel != first.LocalModel || !again.At.Equal(first.At) {
		t.Fatalf("lock was rewritten: %+v → %+v", first, again)
	}
}

// 坏文件报"没锁"。真正的防线是闸门(它每次现场比对当前模型),这个文件只负责
// "重启后仍然记得";拿一个解析不了的文件去拦人,用户既看不懂也解不开。
func TestOALockCorruptFileReadsAsUnlocked(t *testing.T) {
	ws := t.TempDir()
	path := oaLockPath(ws, "s")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ 半个 json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readOALock(ws, "s"); ok {
		t.Fatal("a corrupt lock file must read as unlocked")
	}
}

// —— 换模型的规则(三个入口共用的那条纯函数)——

func TestOAModelSwitchAllowed(t *testing.T) {
	cases := []struct {
		locked, target string
		want           bool
	}{
		{"", "glm-5.1", true},                // 没锁,随便换
		{"qwen3.6-27b", "qwen3.6-27b", true}, // 换成它自己(设置页原样保存)
		{"qwen3.6-27b", "QWEN3.6-27B", true}, // 大小写不敏感
		{"qwen3.6-27b", "glm-5.1", false},    // 换云端 → 拒
		{"qwen3.6-27b", "", false},           // 清空也是换
	}
	for _, tc := range cases {
		if got := oaModelSwitchAllowed(tc.locked, tc.target); got != tc.want {
			t.Errorf("oaModelSwitchAllowed(%q, %q) = %v, want %v", tc.locked, tc.target, got, tc.want)
		}
	}
}

// 拒绝的措辞要说清三件事,尤其是**能怎么办**——少了最后一条,用户只会觉得应用坏了。
func TestOALockedSwitchErrorExplainsWayOut(t *testing.T) {
	msg := oaLockedSwitchError("qwen3.6-27b").Error()
	for _, want := range []string{"qwen3.6-27b", "OA", "历史", "新建"} {
		if !strings.Contains(msg, want) {
			t.Errorf("lock message missing %q: %s", want, msg)
		}
	}
}

// —— 切换时那份配置 ——

// 四处必须一起改。少一处就漏一条:HarmJudgeModel 留着的话,智能模式下每次审批都会
// 把动作描述发给云端判定模型(它是会话级固定的,引擎没有运行时 setter)。
func TestApplyLocalModelConfigClosesEveryPath(t *testing.T) {
	cfg := engine.Config{
		Model:            "glm-5.1",
		MaxContextTokens: 200000,
		HarmJudgeModel:   "glm-5.1",
		DisabledAgents:   []string{"已停用的"},
	}
	local := PassportModel{ID: "qwen3.6-27b", Local: true, ContextTokens: 65536}
	got := applyLocalModelConfig(cfg, local, []string{"钉了云端模型的子代理"})

	if got.Model != "qwen3.6-27b" {
		t.Errorf("Model = %q", got.Model)
	}
	if got.MaxContextTokens != 65536 {
		t.Errorf("MaxContextTokens = %d, want the local model's window", got.MaxContextTokens)
	}
	if got.HarmJudgeModel != "" {
		t.Errorf("HarmJudgeModel = %q, want cleared so it falls back to the local model", got.HarmJudgeModel)
	}
	if !slicesContain(got.DisabledAgents, "钉了云端模型的子代理") {
		t.Errorf("DisabledAgents = %v, want the foreign-model agent disabled", got.DisabledAgents)
	}
	if !slicesContain(got.DisabledAgents, "已停用的") {
		t.Errorf("DisabledAgents = %v, want the pre-existing entry kept", got.DisabledAgents)
	}
}

// 基座没声明窗口大小时不要瞎改预算:0 意味着"未声明",不是"窗口为零"。
func TestApplyLocalModelConfigKeepsBudgetWhenUndeclared(t *testing.T) {
	cfg := engine.Config{Model: "glm-5.1", MaxContextTokens: 200000}
	got := applyLocalModelConfig(cfg, PassportModel{ID: "qwen3.6-27b", Local: true}, nil)
	if got.MaxContextTokens != 200000 {
		t.Fatalf("MaxContextTokens = %d, want the session value kept", got.MaxContextTokens)
	}
}

func TestMergeDisabledIsStableAndDeduped(t *testing.T) {
	got := mergeDisabled([]string{"a", "b", ""}, []string{"b", "c", " "})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("mergeDisabled = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mergeDisabled = %v, want %v", got, want)
		}
	}
}

func slicesContain(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// —— 出网工具的封锁 ——

// 锁定之后,不需要用户点头就能出网的工具一律拒绝。模型可以把 OA 内容拼进搜索词,
// 而联网搜索是自动放行的——切了模型也堵不住这条。
func TestOALockPolicyBlocksEgressOnlyWhenLocked(t *testing.T) {
	locked := ""
	policy := newOALockPolicy(allowAllPolicy{}, func() string { return locked })

	for _, name := range []string{websearch.ToolName, webfetch.Tool{}.Name()} {
		if d := policy.Decide(context.Background(), permissions.Action{ToolName: name}); d.Effect != permissions.EffectAllow {
			t.Fatalf("unlocked session: %s was blocked (%v)", name, d.Effect)
		}
	}

	locked = "qwen3.6-27b"
	for _, name := range []string{websearch.ToolName, webfetch.Tool{}.Name()} {
		d := policy.Decide(context.Background(), permissions.Action{ToolName: name})
		if d.Effect != permissions.EffectDeny {
			t.Fatalf("locked session: %s was allowed to reach the network", name)
		}
		if d.Reason != reasonOALocked {
			t.Errorf("%s denied for %q, want %q", name, d.Reason, reasonOALocked)
		}
	}
	// 其余工具照常透传——封的是出网,不是把会话变成只读。
	for _, name := range []string{"Read", "Bash", "oa_todo"} {
		if d := policy.Decide(context.Background(), permissions.Action{ToolName: name}); d.Effect != permissions.EffectAllow {
			t.Errorf("locked session: %s should still fall through to the inner policy", name)
		}
	}
}

// lockedModel 必须是函数而不是构建时的布尔值:锁是会话**中途**才出现的,
// 取一次值等于永远读到"没锁"。
func TestOALockPolicyReadsLockLive(t *testing.T) {
	locked := ""
	policy := newOALockPolicy(allowAllPolicy{}, func() string { return locked })
	if d := policy.Decide(context.Background(), permissions.Action{ToolName: websearch.ToolName}); d.Effect != permissions.EffectAllow {
		t.Fatal("precondition: unlocked should allow")
	}
	locked = "qwen3.6-27b" // 会话中途读了一次 OA
	if d := policy.Decide(context.Background(), permissions.Action{ToolName: websearch.ToolName}); d.Effect != permissions.EffectDeny {
		t.Fatal("the policy cached the unlocked state; a mid-session lock must take effect at once")
	}
}

type allowAllPolicy struct{}

func (allowAllPolicy) Decide(context.Context, permissions.Action) permissions.Decision {
	return permissions.Allow(permissions.ReasonAllowedRead, "test.allow")
}

// —— 闸门与自动切换的交接 ——

// 闸门放行的那一刻就上锁:等结果回来再锁会留一个窗口,窗口里用户切走模型就漏了。
func TestGatePassLocksSession(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	app.mu.Lock()
	app.sessions["s1"] = &sessionEntry{id: "s1", workspace: ws}
	app.mu.Unlock()

	app.lockSessionToLocalModel("s1", "qwen3.6-27b")

	if got := app.oaLockedModel("s1"); got != "qwen3.6-27b" {
		t.Fatalf("in-memory lock = %q", got)
	}
	if lock, ok := readOALock(ws, "s1"); !ok || lock.LocalModel != "qwen3.6-27b" {
		t.Fatalf("lock was not persisted: %+v %v", lock, ok)
	}
}

// 防重入:切完仍过不了闸时不能再切,否则是"重建→重发→再被拦→再重建"的死循环,
// 而每一轮都真的跑一次模型。
func TestOASwitchRequestedOnlyOnce(t *testing.T) {
	app := New(&recordingSink{})
	app.mu.Lock()
	app.sessions["s1"] = &sessionEntry{id: "s1", lastUserText: "我今天的待办"}
	app.focused = "s1"
	app.mu.Unlock()

	err := app.requestOASwitch("s1", "glm-5.1", "qwen3.6-27b", "tu_1")
	if !strings.Contains(err.Error(), "自动重试") {
		t.Fatalf("first refusal should announce the auto-switch, got %q", err)
	}
	model, text, ok := app.takeOASwitch("s1")
	if !ok || model != "qwen3.6-27b" || text != "我今天的待办" {
		t.Fatalf("takeOASwitch = %q, %q, %v", model, text, ok)
	}
	// 取走之后就没有第二次了。
	if _, _, ok := app.takeOASwitch("s1"); ok {
		t.Fatal("the pending switch was not cleared")
	}
	// 再被拦时只回普通拒绝,不再排队。
	err = app.requestOASwitch("s1", "glm-5.1", "qwen3.6-27b", "tu_1")
	if strings.Contains(err.Error(), "自动重试") {
		t.Fatal("a second auto-switch was scheduled; that is the reentry loop")
	}
	if _, _, ok := app.takeOASwitch("s1"); ok {
		t.Fatal("a second switch was queued")
	}
}

// 重建会新建 sessionEntry（registerSessionLocked 每次都 &sessionEntry{}），三个 OA
// 字段全部归零。不把它们过继过去的后果有两个，而且都不报错：
//
//	· oaSwitchDone 归零 → 闸门再误判一次就"切换→重发→再切"无限重建，每轮真跑一次模型；
//	· oaLocalModel 归零 → 出网工具重新放行，而历史里已经有 OA 数据了。
//
// 这是真实事故（日志里连着三轮 "已切换到本地模型"，模型一直在重试）。
func TestOAStateSurvivesRebuild(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	app.mu.Lock()
	// 模拟重建后的样子：全新条目，三个字段都是零值。
	app.sessions["rebuilt"] = &sessionEntry{id: "rebuilt", workspace: ws}
	app.focused = "rebuilt"
	app.mu.Unlock()

	app.adoptOAStateAfterRebuild("qwen3.6-27b")

	app.mu.Lock()
	e := app.sessions["rebuilt"]
	done, locked := e.oaSwitchDone, e.oaLocalModel
	app.mu.Unlock()

	if !done {
		t.Error("防重入标记没有过继 —— 下一次闸门误判就会无限重建")
	}
	if locked != "qwen3.6-27b" {
		t.Errorf("OA 锁没有过继（拿到 %q）—— 出网工具会重新放行", locked)
	}
	// 落盘那份也要补上，否则关掉重开就不锁了。
	if lock, ok := readOALock(ws, "rebuilt"); !ok || lock.LocalModel != "qwen3.6-27b" {
		t.Errorf("重建后没有补写落盘锁: %+v %v", lock, ok)
	}
}

// 已经锁在某个模型上时，过继不该把它改掉（锁是一次性事实）。
func TestAdoptKeepsExistingLock(t *testing.T) {
	app := New(&recordingSink{})
	app.mu.Lock()
	app.sessions["s"] = &sessionEntry{id: "s", workspace: t.TempDir(), oaLocalModel: "已锁定的模型"}
	app.focused = "s"
	app.mu.Unlock()

	app.adoptOAStateAfterRebuild("另一个模型")

	if got := app.oaLockedModel("s"); got != "已锁定的模型" {
		t.Fatalf("已有的锁被改成了 %q", got)
	}
}

// 后台会话不自动切:重建会切聚焦与工作区,对着用户没在看的那条会话这么干,等于
// 把界面从他手里抽走。数据依然被闸门拦住,只是不自动切。
func TestOASwitchSkipsBackgroundSession(t *testing.T) {
	app := New(&recordingSink{})
	app.mu.Lock()
	app.sessions["bg"] = &sessionEntry{id: "bg", lastUserText: "待办"}
	app.focused = "other"
	app.mu.Unlock()

	err := app.requestOASwitch("bg", "glm-5.1", "qwen3.6-27b", "tu_1")
	if strings.Contains(err.Error(), "自动重试") {
		t.Fatal("a background session scheduled an auto-switch")
	}
	if _, _, ok := app.takeOASwitch("bg"); ok {
		t.Fatal("a background session queued a switch")
	}
}

// —— 恢复会话时把模型钉回去 ——

// "关掉重开"是绕过锁最现成的办法,这条钉住它被堵上了。
func TestRestoreOALockPinsModelOnResume(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	if err := writeOALock(ws, "sess_old", "qwen3.6-27b"); err != nil {
		t.Fatal(err)
	}

	got := app.restoreOALockHeld(engine.Config{
		CWD: ws, Resume: "sess_old",
		Model: "glm-5.1", HarmJudgeModel: "glm-5.1",
	})
	if got.Model != "qwen3.6-27b" {
		t.Fatalf("resumed model = %q, want the locked local model", got.Model)
	}
	if got.HarmJudgeModel != "" {
		t.Errorf("HarmJudgeModel = %q, want cleared on resume too", got.HarmJudgeModel)
	}
}

// 新会话没有历史，也就没有 OA 数据；不该被别的会话的锁影响。
func TestRestoreOALockIgnoresFreshSession(t *testing.T) {
	app := New(&recordingSink{})
	ws := t.TempDir()
	if err := writeOALock(ws, "sess_old", "qwen3.6-27b"); err != nil {
		t.Fatal(err)
	}
	got := app.restoreOALockHeld(engine.Config{CWD: ws, Model: "glm-5.1"})
	if got.Model != "glm-5.1" {
		t.Fatalf("a fresh session was pinned to %q", got.Model)
	}
}

// —— 两个改模型的入口 ——

// SwitchModel 与 SaveSettings 共用同一条规则。这条测的是"锁着时切云端模型被拒",
// 以及自定义模型(直连第三方端点)一律被拒。
func TestSwitchModelRefusedWhenOALocked(t *testing.T) {
	app := New(&recordingSink{})
	app.mu.Lock()
	app.sessions["s1"] = &sessionEntry{id: "s1", oaLocalModel: "qwen3.6-27b"}
	app.focused = "s1"
	app.mu.Unlock()

	_, err := app.SwitchModel("s1", "platform", "glm-5.1")
	if err == nil || !strings.Contains(err.Error(), "qwen3.6-27b") {
		t.Fatalf("switching to a cloud model was allowed on a locked session: %v", err)
	}
	// 自定义连接直连第三方端点,锁着时一律拒——而且必须是**被锁拦下**,不能是
	// "那个自定义模型不存在"之类的巧合(那样这条测试就什么也没证明)。
	_, err = app.SwitchModel("s1", "custom", "我的 Ollama")
	if err == nil || !strings.Contains(err.Error(), "qwen3.6-27b") {
		t.Fatalf("custom connection should be refused by the OA lock, got %v", err)
	}
}
