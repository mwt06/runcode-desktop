package desktop

// 多会话**并行**的端到端验证：两条真的引擎会话（会话由 host.Manager 建、回合跑在
// 它的 goroutine 上、信封由它按会话编号），只把"跑一个回合"这一步换成可控的替身。
//
// 这是 P0/P1 一直欠着的那条。此前测到的只是寻址层（命令打给谁就落在谁身上），
// 而"两个回合真的同时在跑、事件不串台"要有两条活着的会话才谈得上——真会话需要
// 一个连得上的 provider，所以 New 之外多了一个 newWithBuild 口子（引擎的
// host.BuildFunc 本来就写着 tests inject a fake）。

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
)

// fakeHostSession 是一条"回合会一直跑到被放行"的会话。
//
// 它只替换执行，不替换编号、事件发射与回合记账——那些正是要验的东西，由真的
// Manager 负责。
type fakeHostSession struct {
	id      string
	cwd     string        // 会话的工作区，与真会话一样从 Status 报出来
	started chan string   // 回合真的开跑了
	release chan struct{} // 放它收尾
}

func newFakeHostSession() *fakeHostSession {
	return &fakeHostSession{started: make(chan string, 4), release: make(chan struct{})}
}

func (s *fakeHostSession) RunTurn(ctx context.Context, text string) (turn.Result, error) {
	s.started <- text
	select {
	case <-s.release:
		return turn.Result{FinalStopReason: llm.StopReasonEndTurn}, nil
	case <-ctx.Done():
		// 被打断：与真会话一样返回上下文错误，Manager 据此发 turn:error。
		return turn.Result{}, ctx.Err()
	}
}

func (s *fakeHostSession) RunTurnWithImages(ctx context.Context, text string, _ []llm.ImageSource) (turn.Result, error) {
	return s.RunTurn(ctx, text)
}
func (s *fakeHostSession) Inject(string, []llm.ImageSource) error { return nil }
func (s *fakeHostSession) SessionID() string                      { return s.id }
func (s *fakeHostSession) Status() engine.Status {
	return engine.Status{SessionID: s.id, CWD: s.cwd}
}

func (s *fakeHostSession) History() []llm.Message         { return nil }
func (s *fakeHostSession) EstimateContextTokens() int     { return 0 }
func (s *fakeHostSession) SetModel(string) error          { return nil }
func (s *fakeHostSession) SetPermissionMode(string) error { return nil }
func (s *fakeHostSession) SetPlanMode(bool)               {}
func (s *fakeHostSession) SetThinkingEffort(string) error { return nil }
func (s *fakeHostSession) SetReasoningScenario(string)    {}
func (s *fakeHostSession) Close(context.Context) error    { return nil }

// newParallelApp 造一个用替身建会话的 App，并把每次 Build 出来的替身交给调用方。
func newParallelApp(t *testing.T) (*App, *recordingSink, chan *fakeHostSession) {
	t.Helper()
	sink := &recordingSink{}
	built := make(chan *fakeHostSession, 8)
	app := newWithBuild(sink, func(cfg engine.Config, _ engine.Options) (host.Session, error) {
		s := newFakeHostSession()
		s.id, s.cwd = cfg.SessionID, cfg.CWD
		built <- s
		return s, nil
	})
	return app, sink, built
}

// openParallelSession 开一条会话并登记进外壳的会话表，**不关掉别的会话**。
//
// 走的是 registerSessionLocked——与生产路径同一个登记口，只是不带"先关掉当前那条"
// 那道单会话策略。多会话 UI（P2）要的正是这个组合。
func openParallelSession(t *testing.T, a *App, built chan *fakeHostSession) (string, *fakeHostSession) {
	t.Helper()
	cwd := t.TempDir()
	id, _, err := a.mgr.Create(context.Background(), engine.Config{CWD: cwd, Model: "fake"})
	if err != nil {
		t.Fatalf("建会话: %v", err)
	}
	a.mu.Lock()
	edits, plans, emit := a.pendingEdits, a.pendingPlans, a.pendingEmit
	a.pendingEdits, a.pendingPlans, a.pendingEmit = nil, nil, nil
	a.registerSessionLocked(id, cwd, edits, plans, emit)
	a.mu.Unlock()

	select {
	case s := <-built:
		return id, s
	case <-time.After(5 * time.Second):
		t.Fatal("Build 没被调用")
		return "", nil
	}
}

func waitStarted(t *testing.T, s *fakeHostSession, who string) {
	t.Helper()
	select {
	case <-s.started:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s 的回合没有开跑", who)
	}
}

// turnEndSessions 收集所有 turn:end 信封的会话 id，按到达顺序。
func turnEndSessions(sink *recordingSink) []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var out []string
	for _, ev := range sink.events {
		if ev.name != EventTurnEnd {
			continue
		}
		if env, ok := ev.data.(hostproto.Envelope); ok {
			out = append(out, env.SessionID)
		}
	}
	return out
}

func waitTurnEnds(t *testing.T, sink *recordingSink, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := turnEndSessions(sink); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("只等到 %d 条 turn:end，期望 %d 条", len(turnEndSessions(sink)), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func turnActiveOf(a *App, id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entryLocked(id)
	return e != nil && e.turnActive
}

// TestTwoSessionsRunTurnsInParallel 是这件事的核心断言：两个回合**同时**在跑，
// 各自的在途标记独立，回合结束的信封只落在自己那条会话上。
func TestTwoSessionsRunTurnsInParallel(t *testing.T) {
	app, sink, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, sessA := openParallelSession(t, app, built)
	idB, sessB := openParallelSession(t, app, built)
	if idA == idB {
		t.Fatal("两条会话拿到了同一个 id")
	}

	// 显式寻址各发一条。第二条能被接受本身就是结论：单会话时代这里会因为
	// "已经有回合在跑"而被拒。
	if err := app.SendMessage(idA, "A 的活"); err != nil {
		t.Fatalf("给 A 发消息: %v", err)
	}
	waitStarted(t, sessA, "A")
	if err := app.SendMessage(idB, "B 的活"); err != nil {
		t.Fatalf("给 B 发消息: %v", err)
	}
	waitStarted(t, sessB, "B")

	// 此刻两个回合都真的在跑。
	if !turnActiveOf(app, idA) || !turnActiveOf(app, idB) {
		t.Fatalf("两条会话都该在跑：A=%v B=%v", turnActiveOf(app, idA), turnActiveOf(app, idB))
	}

	// 只放行 A。B 必须不受影响——这是"并行"与"排队"的分水岭。
	close(sessA.release)
	ends := waitTurnEnds(t, sink, 1)
	if ends[0] != idA {
		t.Fatalf("第一条 turn:end 的会话是 %q，应为 A(%q)", ends[0], idA)
	}
	deadline := time.Now().Add(2 * time.Second)
	for turnActiveOf(app, idA) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if turnActiveOf(app, idA) {
		t.Fatal("A 的回合结束了，在途标记没清")
	}
	if !turnActiveOf(app, idB) {
		t.Fatal("A 的回合结束把 B 的在途标记也清了——记账串台")
	}

	// 再放行 B。
	close(sessB.release)
	ends = waitTurnEnds(t, sink, 2)
	if ends[1] != idB {
		t.Fatalf("第二条 turn:end 的会话是 %q，应为 B(%q)", ends[1], idB)
	}
}

// TestInterruptOnlyStopsTheAddressedSession 盯住打断只打断指定的那条。
//
// 打断走的是 Manager.Interrupt(id)：它取消那条会话的回合上下文，并拒掉它挂着的
// 授权。按"当前是哪条"去打断，用户在 A 会话按下停止就会把 B 的活也掐了。
func TestInterruptOnlyStopsTheAddressedSession(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, sessA := openParallelSession(t, app, built)
	idB, sessB := openParallelSession(t, app, built)

	if err := app.SendMessage(idA, "A"); err != nil {
		t.Fatalf("给 A 发消息: %v", err)
	}
	waitStarted(t, sessA, "A")
	if err := app.SendMessage(idB, "B"); err != nil {
		t.Fatalf("给 B 发消息: %v", err)
	}
	waitStarted(t, sessB, "B")

	if err := app.Interrupt(idA); err != nil {
		t.Fatalf("打断 A: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for turnActiveOf(app, idA) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if turnActiveOf(app, idA) {
		t.Fatal("A 被打断后在途标记没清")
	}
	if !turnActiveOf(app, idB) {
		t.Fatal("打断 A 把 B 的回合也停了")
	}

	close(sessB.release)
}

// TestSendMessageRejectsUnknownSession 盯住寻址错了要报错，而不是退回"当前那条"。
//
// 这是最坏的一种失败：把 A 的消息发给了 B，两边都不会报错，用户只会看到"我明明
// 在这条对话里发的，怎么出现在那条里"。
func TestSendMessageRejectsUnknownSession(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, sessA := openParallelSession(t, app, built)

	if err := app.SendMessage("no-such-session", "喂"); err == nil {
		t.Fatal("给不存在的会话发消息必须报错，绝不能退回聚焦会话")
	}
	select {
	case text := <-sessA.started:
		t.Fatalf("消息落到了聚焦会话 %q 上：%q", idA, text)
	case <-time.After(200 * time.Millisecond):
	}
}

// ---- P2：多会话的开 / 切 / 关 ------------------------------------------------

// TestOpenSessionKeepsExistingOnesOpen 盯住"加开一条"不碰已经开着的。
//
// 这是与 NewSession 的分水岭：那个是替换式打开（先关掉当前那条），并行要的正是
// 不带那道策略的这一半。改错了不会报错，只会表现为"一开新会话，正在跑的那条
// 就没了"。
func TestOpenSessionKeepsExistingOnesOpen(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	first, firstSess := openParallelSession(t, app, built)
	// OpenSession 走 a.config（它带的是"下一条会话用什么连接"），测试里补齐。
	// a.workspace 由 registerSessionLocked 顺着 focusLocked 一起设好了。
	app.mu.Lock()
	app.config = engine.Config{Model: "fake", CWD: app.workspace}
	app.mu.Unlock()

	if err := app.SendMessage(first, "在跑"); err != nil {
		t.Fatalf("给第一条发消息: %v", err)
	}
	waitStarted(t, firstSess, "first")

	info, err := app.OpenSession("")
	if err != nil {
		t.Fatalf("加开一条会话: %v", err)
	}
	if info.SessionID == first {
		t.Fatal("加开的会话拿到了同一个 id")
	}
	<-built // 第二条的替身

	open := app.OpenSessions()
	if len(open) != 2 {
		t.Fatalf("应当有两条开着的会话，实际 %d 条", len(open))
	}
	if !turnActiveOf(app, first) {
		t.Fatal("加开一条会话把正在跑的那条关掉了")
	}
	// 新开的那条成为聚焦。
	if app.focusedSessionID() != info.SessionID {
		t.Fatalf("聚焦应落在新开的会话上，实际 %q", app.focusedSessionID())
	}
	close(firstSess.release)
}

// TestOpenSessionInAnotherWorkspace 盯住 P3 的核心：在**另一个目录**开一条会话，
// 已经开着的（而且正在跑的）那条一条都不动。
//
// 它取代了 SwitchWorkspace——那个是"关掉当前会话、在新目录重开一条"，换个目录看看
// 就得丢掉手上正在跑的回合。改坏了的表现是并行两个项目时，切过去的一瞬间另一个
// 项目的任务无声消失。
func TestOpenSessionInAnotherWorkspace(t *testing.T) {
	// 换目录会把它记进最近工作区（MRU）——写的是用户配置目录，测试里隔离掉。
	isolateConfigDir(t)
	isolateConfigDir(t)

	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	first, firstSess := openParallelSession(t, app, built)
	app.mu.Lock()
	firstWS := app.sessions[first].workspace
	app.config = engine.Config{Model: "fake", CWD: firstWS}
	app.mu.Unlock()

	if err := app.SendMessage(first, "在跑"); err != nil {
		t.Fatalf("给第一条发消息: %v", err)
	}
	waitStarted(t, firstSess, "first")

	other := t.TempDir()
	info, err := app.OpenSession(other)
	if err != nil {
		t.Fatalf("在另一个目录开一条会话: %v", err)
	}
	<-built

	if info.CWD != other {
		t.Fatalf("新会话应当开在 %q，实际 %q", other, info.CWD)
	}
	if !turnActiveOf(app, first) {
		t.Fatal("换目录把正在跑的那条会话关掉了——这正是 SwitchWorkspace 的老毛病")
	}
	if len(app.OpenSessions()) != 2 {
		t.Fatalf("两条会话都该开着，实际 %d 条", len(app.OpenSessions()))
	}
	// 聚焦落到新会话上，工作区跟着搬——工作区寻址的那批命令（文件列表、技能、
	// MCP、记忆）读的就是它。
	app.mu.Lock()
	focusedWS, entryWS := app.workspace, app.sessions[info.SessionID].workspace
	app.mu.Unlock()
	if focusedWS != other || entryWS != other {
		t.Fatalf("工作区没跟着搬：a.workspace=%q 条目=%q，应为 %q", focusedWS, entryWS, other)
	}
	// 第一条会话记着的还是它自己的目录：会话与目录一一对应，终生不变。
	app.mu.Lock()
	firstStill := app.sessions[first].workspace
	app.mu.Unlock()
	if firstStill != firstWS {
		t.Fatalf("第一条会话的目录被改成了 %q，应为 %q", firstStill, firstWS)
	}
	// 预览服务器起在**新会话自己的目录**上。按目录共享与引用计数本身另有测试
	// （preview_lifecycle_test.go），这里只认"开在哪个目录就服务哪个目录"。
	app.mu.Lock()
	ref := app.previews[other]
	app.mu.Unlock()
	if ref == nil {
		t.Fatalf("新工作区 %q 没有预览服务器", other)
	}
	close(firstSess.release)
}

// TestResumeKeepsRunningSessionOpen 盯住"点开一条历史对话"是**加开一条**，不会把
// 正在跑的那条关掉。
//
// 这是同一个坑的第三次报障：「新建对话」「换工作区」都改成加开之后，只剩恢复历史
// 还是替换式打开——聚焦在正跑着活的会话上，点一下「最近对话」，活当场停。
func TestResumeKeepsRunningSessionOpen(t *testing.T) {
	isolateConfigDir(t)
	isolateConfigDir(t)

	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	running, runningSess := openParallelSession(t, app, built)
	app.mu.Lock()
	app.config = engine.Config{Model: "fake", CWD: app.workspace}
	app.mu.Unlock()

	if err := app.SendMessage(running, "长任务"); err != nil {
		t.Fatalf("发消息: %v", err)
	}
	waitStarted(t, runningSess, "running")

	res, err := app.ResumeSession("另一条历史对话")
	if err != nil {
		t.Fatalf("恢复历史对话: %v", err)
	}
	<-built
	if res.Info.SessionID == running {
		t.Fatal("恢复出来的是同一条会话")
	}
	if !turnActiveOf(app, running) {
		t.Fatal("点开一条历史对话把正在跑的那条停掉了")
	}
	if got := len(app.OpenSessions()); got != 2 {
		t.Fatalf("两条会话都该开着，实际 %d 条", got)
	}
	close(runningSess.release)
}

// TestResumeUsesTheFocusedWorkspace 盯住恢复历史对话时用的是**聚焦这条会话的目录**，
// 而不是 a.config 里那份可能已经过期的 CWD。
//
// 真踩过：在 dir2 开过一条会话之后 a.config.CWD 就是 dir2，聚焦切回 dir1 的会话时
// 它并不会跟着回来。此时从 dir1 的「最近对话」点一条，旧代码会拿着 dir1 的会话 id
// 去 dir2 的存储里恢复——那里没有这条记录，引擎照这个 id 在 dir2 建了一条**空**对话，
// 而 dir1 那条正跑着的会话已经被替换式打开关掉了。用户看到的是"点了最近对话，
// 内容空了，原来那条也没了"。
func TestResumeUsesTheFocusedWorkspace(t *testing.T) {
	isolateConfigDir(t)
	isolateConfigDir(t)

	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, _ := openParallelSession(t, app, built)
	app.mu.Lock()
	dir1 := app.sessions[idA].workspace
	app.config = engine.Config{Model: "fake", CWD: dir1}
	app.mu.Unlock()

	// 在另一个目录开一条：a.config.CWD 从此指向 dir2。
	dir2 := t.TempDir()
	if _, err := app.OpenSession(dir2); err != nil {
		t.Fatalf("在 dir2 开一条会话: %v", err)
	}
	<-built
	app.mu.Lock()
	stale := app.config.CWD
	app.mu.Unlock()
	if stale != dir2 {
		t.Fatalf("前提不成立：a.config.CWD 应为 dir2(%q)，实际 %q", dir2, stale)
	}

	// 聚焦切回 dir1 的会话，再恢复 dir1 里的一条历史对话。
	if _, err := app.FocusSession(idA); err != nil {
		t.Fatalf("聚焦回 A: %v", err)
	}
	if _, err := app.ResumeSession("dir1-的某条历史"); err != nil {
		t.Fatalf("恢复历史对话: %v", err)
	}
	resumed := <-built
	if resumed.cwd != dir1 {
		t.Fatalf("恢复开在了 %q，应为聚焦会话的目录 %q", resumed.cwd, dir1)
	}
}

// TestOpenSessionRejectsMissingDirectory 盯住目录不存在时**什么都不动**：
// 报错，聚焦不变，也不会把当前会话关掉。
func TestOpenSessionRejectsMissingDirectory(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	first, _ := openParallelSession(t, app, built)
	app.mu.Lock()
	app.config = engine.Config{Model: "fake", CWD: app.workspace}
	app.mu.Unlock()

	if _, err := app.OpenSession(filepath.Join(t.TempDir(), "并不存在")); err == nil {
		t.Fatal("目录不存在时应当报错")
	}
	if app.focusedSessionID() != first {
		t.Fatalf("失败的打开不该改变聚焦，实际 %q", app.focusedSessionID())
	}
	if len(app.OpenSessions()) != 1 {
		t.Fatalf("失败的打开不该动会话表，实际 %d 条", len(app.OpenSessions()))
	}
}

// TestFocusSessionMovesWorkspace 盯住聚焦会把工作区一并搬过去。
//
// 聚焦不只是界面高亮：工作区寻址的那批命令（文件列表、技能、子代理、MCP、记忆）
// 读的是 a.workspace。不搬的话，切到另一个目录的会话后文件浏览器还停在上一条
// 会话的目录上——而且不会有任何报错。
func TestFocusSessionMovesWorkspace(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, _ := openParallelSession(t, app, built)
	idB, _ := openParallelSession(t, app, built)

	app.mu.Lock()
	wsA := app.sessions[idA].workspace
	wsB := app.sessions[idB].workspace
	app.mu.Unlock()
	if wsA == wsB {
		t.Fatal("两条会话应当各在各的临时目录里")
	}

	if _, err := app.FocusSession(idA); err != nil {
		t.Fatalf("聚焦到 A: %v", err)
	}
	if app.focusedSessionID() != idA {
		t.Fatalf("聚焦没切过去：%q", app.focusedSessionID())
	}
	if got := app.workspaceDir(); got != wsA {
		t.Fatalf("聚焦到 A 之后工作区是 %q，应为 A 的 %q", got, wsA)
	}

	if _, err := app.FocusSession("no-such-session"); err == nil {
		t.Fatal("聚焦到不存在的会话必须报错")
	}
	if app.focusedSessionID() != idA {
		t.Fatal("聚焦失败不该改动当前聚焦")
	}
}

// TestCloseSessionOnlyClosesThatOne 盯住关掉一条不影响另一条，且聚焦会落到还活着
// 的那条上——否则用户关掉一个标签页，界面会掉进"没有会话"的空态。
func TestCloseSessionOnlyClosesThatOne(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, _ := openParallelSession(t, app, built)
	idB, sessB := openParallelSession(t, app, built)

	if err := app.SendMessage(idB, "B 在跑"); err != nil {
		t.Fatalf("给 B 发消息: %v", err)
	}
	waitStarted(t, sessB, "B")

	// 此刻聚焦在 B（后开的那条）。关掉 A：B 不受影响。
	if err := app.CloseSession(idA); err != nil {
		t.Fatalf("关掉 A: %v", err)
	}
	if open := app.OpenSessions(); len(open) != 1 || open[0].SessionID != idB {
		t.Fatalf("关掉 A 之后开着的应只剩 B，实际 %+v", open)
	}
	if !turnActiveOf(app, idB) {
		t.Fatal("关掉 A 把 B 的回合也停了")
	}
	// A 的条目要被移除:它已经从界面上消失,留着只会让列表多出一条死会话。
	if _, err := app.entryOf(idA); err == nil {
		t.Fatal("关掉的会话条目应当被移除")
	}

	// 再关掉聚焦的那条（空串），此时没有别的会话可落脚。
	close(sessB.release)
	if err := app.CloseSession(""); err != nil {
		t.Fatalf("关掉聚焦会话: %v", err)
	}
	if open := app.OpenSessions(); len(open) != 0 {
		t.Fatalf("全部关掉后仍有 %d 条开着", len(open))
	}
	if app.focusedSessionID() != "" {
		t.Fatalf("没有会话时聚焦应为空，实际 %q", app.focusedSessionID())
	}
}

// TestCloseSessionRefocusesToASurvivor 盯住关掉聚焦那条时，聚焦落到还开着的另一条。
func TestCloseSessionRefocusesToASurvivor(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, _ := openParallelSession(t, app, built)
	idB, _ := openParallelSession(t, app, built) // 后开的成为聚焦

	if app.focusedSessionID() != idB {
		t.Fatalf("前提不成立：聚焦应在 B，实际 %q", app.focusedSessionID())
	}
	if err := app.CloseSession(idB); err != nil {
		t.Fatalf("关掉聚焦会话: %v", err)
	}
	if app.focusedSessionID() != idA {
		t.Fatalf("关掉聚焦会话后应落到 A，实际 %q", app.focusedSessionID())
	}
	app.mu.Lock()
	ws := app.workspace
	wsA := app.sessions[idA].workspace
	app.mu.Unlock()
	if ws != wsA {
		t.Fatal("改聚焦时工作区没跟着搬")
	}
}

// TestResumeAlreadyOpenSessionFocusesIt 盯住「恢复一条已经开着、但不聚焦的会话」。
//
// 这是多会话撞上「替换式打开」留下的坑：侧栏「最近对话」与「打开中」会有重合，
// 点到一条正开着的会话时，旧路径会先关掉聚焦的那条，再拿目标 id 去 Manager.Create，
// 而目标还在表里 —— 撞 host.ErrSessionExists（"session already exists"），并且刚才
// 聚焦的那条已经白白关掉了。正确行为是只把聚焦切过去。
func TestResumeAlreadyOpenSessionFocusesIt(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, fakeA := openParallelSession(t, app, built)
	idB, _ := openParallelSession(t, app, built) // 后开的成为聚焦

	if app.focusedSessionID() != idB {
		t.Fatalf("前提不成立：聚焦应在 B，实际 %q", app.focusedSessionID())
	}
	// ResumeSession 复用 a.config 的 provider/模型，测试里给个能过门槛的。
	app.mu.Lock()
	app.config = engine.Config{CWD: app.workspace, Model: "fake"}
	app.mu.Unlock()

	res, err := app.ResumeSession(idA)
	if err != nil {
		t.Fatalf("恢复已经开着的会话不该报错: %v", err)
	}
	if res.Info.SessionID != idA {
		t.Fatalf("返回的应是 A 的状态，实际 %q", res.Info.SessionID)
	}
	if app.focusedSessionID() != idA {
		t.Fatalf("聚焦应切到 A，实际 %q", app.focusedSessionID())
	}
	// B 必须还开着：把它连坐关掉正是这次要修的那半个后果。
	if got := len(app.OpenSessions()); got != 2 {
		t.Fatalf("两条会话都该还开着，实际 %d 条", got)
	}
	// 而且 A 是**原来那条**，不是同 id 重建出来的新会话（重建会再走一次 Build）。
	sess, err := app.mgr.Session(idA)
	if err != nil {
		t.Fatalf("取 A 的句柄: %v", err)
	}
	if sess != host.Session(fakeA) {
		t.Fatal("A 被重建了，应当原地聚焦")
	}
}

// TestFocusAndListDoNotStopARunningTurn 盯住"在会话之间切换"本身不会碰任何会话的
// 回合——切聚焦、回读状态、列一遍工作区里存着的会话，都只是读。
//
// 报障说的是"在「打开中」里切一下，正在跑的活就停了"。这条测试把界面切换时后端
// 实际发生的那几个调用按顺序走一遍，用来把范围切开：它绿，问题就不在这一层。
func TestFocusAndListDoNotStopARunningTurn(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	idA, sessA := openParallelSession(t, app, built)
	idB, _ := openParallelSession(t, app, built)

	if err := app.SendMessage(idA, "长任务"); err != nil {
		t.Fatalf("给 A 发消息: %v", err)
	}
	waitStarted(t, sessA, "A")

	// 界面切换一次 = FocusSession + Status + ListSessions（「最近对话」按工作区列，
	// 切过去要重读）。
	if _, err := app.FocusSession(idB); err != nil {
		t.Fatalf("聚焦到 B: %v", err)
	}
	if _, err := app.Status(""); err != nil {
		t.Fatalf("回读状态: %v", err)
	}
	if _, err := app.ListSessions(); err != nil {
		t.Fatalf("列最近对话: %v", err)
	}
	// 再切回去。
	if _, err := app.FocusSession(idA); err != nil {
		t.Fatalf("聚焦回 A: %v", err)
	}
	if _, err := app.ListSessions(); err != nil {
		t.Fatalf("列最近对话: %v", err)
	}

	if !turnActiveOf(app, idA) {
		t.Fatal("切换会话把正在跑的回合停了")
	}
	if got := len(app.OpenSessions()); got != 2 {
		t.Fatalf("两条会话都该还开着，实际 %d 条", got)
	}
	close(sessA.release)
}

// TestOpenSessionsIsStablyOrdered 盯住会话列表的顺序**稳定**：按打开先后，不随
// map 的遍历序变。
//
// 不排的表现很难往这上面想：界面每回读一次列表就重新洗一次牌，用户点一行、
// 列表跳一下、再点同一个位置就点到了别人身上——而每行右侧就是关闭按钮。
func TestOpenSessionsIsStablyOrdered(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	var want []string
	for range 6 {
		id, _ := openParallelSession(t, app, built)
		want = append(want, id)
	}
	// 多读几遍：map 遍历的随机性不是每次都体现，一次相等证明不了什么。
	for round := range 20 {
		got := make([]string, 0, len(want))
		for _, s := range app.OpenSessions() {
			got = append(got, s.SessionID)
		}
		if len(got) != len(want) {
			t.Fatalf("第 %d 遍：%d 条，期望 %d 条", round, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("第 %d 遍顺序变了：第 %d 行是 %q，应为 %q", round, i, got[i], want[i])
			}
		}
	}
}

// TestClosingTheLastSessionKeepsTheWorkspace 盯住关掉最后一条会话之后还能在**原地**
// 再开一条。
//
// 界面据此在"关掉最后一条"时给用户一条新的空会话，而不是把他退回首屏——首屏会整个
// 重挂载、重跑登录门与工作区选择，而他做的只是把手上这条对话收掉，工作区和连接
// 一样都没变。这条测试盯的是后端那半：关掉最后一条不会顺手把工作区也清了。
func TestClosingTheLastSessionKeepsTheWorkspace(t *testing.T) {
	app, _, built := newParallelApp(t)
	defer func() { _ = app.mgr.CloseAll(context.Background()) }()

	id, _ := openParallelSession(t, app, built)
	app.mu.Lock()
	ws := app.sessions[id].workspace
	app.config = engine.Config{Model: "fake", CWD: ws}
	app.mu.Unlock()

	if err := app.CloseSession(id); err != nil {
		t.Fatalf("关掉最后一条会话: %v", err)
	}
	if got := len(app.OpenSessions()); got != 0 {
		t.Fatalf("应当一条都不剩，实际 %d 条", got)
	}

	info, err := app.OpenSession("")
	if err != nil {
		t.Fatalf("关掉最后一条之后开不出新会话: %v", err)
	}
	<-built
	if info.CWD != ws {
		t.Fatalf("新会话开在了 %q，应为原来的工作区 %q", info.CWD, ws)
	}
}
