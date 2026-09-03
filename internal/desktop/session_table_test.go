package desktop

import "testing"

// focusOn 在会话表里摆一个条目并聚焦它——测试用的最小替身,免得为了摸一个字段
// 就去开真会话(那需要一个连得上的 provider)。
func focusOn(a *App, id, workspace string) *sessionEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessions == nil { // 直接用 &App{} 字面量造的测试对象没建表
		a.sessions = map[string]*sessionEntry{}
	}
	e := &sessionEntry{id: id, workspace: workspace, edits: a.idleEdits, plans: a.idlePlans}
	a.sessions[id] = e
	a.focused = id
	return e
}

// 这一组盯住会话表的不变量——P0 把「唯一活动会话」换成「一张表 + 一个聚焦 id」，
// 后面几期都建在这些性质上，破了会以很难查的方式表现出来（事件记在别人头上、
// 卡片突然空掉）。

// TestNoteTurnDoneIsPerSession 盯住回合结束只清自己那条记账。
//
// 原来的实现是「信封的会话 id 等于当前会话才清」，多会话下后台会话的回合结束
// 会被整个丢掉，它的 turnActive 永远挂着——界面上表现为那个会话一直"在跑"。
func TestNoteTurnDoneIsPerSession(t *testing.T) {
	a := New(&recordingSink{})
	bg := focusOn(a, "background", "")
	fg := focusOn(a, "foreground", "") // 后调用的成为聚焦会话

	a.mu.Lock()
	bg.turnActive, fg.turnActive = true, true
	a.mu.Unlock()

	a.noteTurnDone("background")

	a.mu.Lock()
	bgActive, fgActive := bg.turnActive, fg.turnActive
	a.mu.Unlock()
	if bgActive {
		t.Fatal("后台会话的回合结束没有被记下")
	}
	if !fgActive {
		t.Fatal("后台会话的回合结束把聚焦会话的在途标记也清了")
	}
	if !a.turnInFlight() {
		t.Fatal("turnInFlight 报的应该是聚焦会话的状态")
	}
}

// TestClosedSessionKeepsStoresUntilReplaced 盯住「关闭后仍可复审」这条既有行为。
//
// 会话关掉之后「已编辑」卡片还要能点开看、能撤销，直到下一个会话把它替换掉。
// 靠的是条目留在表里、focused 不动；只有开新会话时才清。改法上很容易顺手在
// close 里 delete 掉，那样卡片会当场变空，而且不会有任何报错。
func TestClosedSessionKeepsStoresUntilReplaced(t *testing.T) {
	a := New(&recordingSink{})
	ws := t.TempDir()
	e := focusOn(a, "s1", ws)
	own := newEditStore()
	a.mu.Lock()
	e.edits = own
	a.mu.Unlock()

	a.closeCurrentHeld()

	// 关了：命令层该看成"没有会话"。
	if _, err := a.liveEntry(); err == nil {
		t.Fatal("会话已关闭，liveEntry 仍然给出条目")
	}
	if id := a.liveSessionIDOrEmpty(); id != "" {
		t.Fatalf("会话已关闭，liveSessionIDOrEmpty = %q，应为空", id)
	}
	// 但存储还在，卡片还能用。
	if got := a.editStore(); got != own {
		t.Fatal("会话关闭后编辑存储就丢了——「已编辑」卡片会当场空掉")
	}
	if a.focusedSessionID() != "s1" {
		t.Fatal("关闭不该改动 focused：它是卡片还能找到数据源的原因")
	}

	// 下一个会话开出来才清。
	a.mu.Lock()
	a.dropClosedLocked()
	n := len(a.sessions)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("新会话开出来后仍留着 %d 个已关闭条目", n)
	}
	if got := a.editStore(); got != a.idleEdits {
		t.Fatal("条目清掉后 editStore 应退回兜底存储")
	}
}

// TestFocusedStoresNeverNil 盯住「永远不为 nil」这条契约。
//
// 第一个会话开出来之前就有命令会读这两个存储（界面加载时就会问一次「已编辑」
// 和计划状态）。返回 nil 的话那些路径要么崩要么各自判空，两种都不该发生。
func TestFocusedStoresNeverNil(t *testing.T) {
	a := New(&recordingSink{})

	edits, plans := a.focusedStores()
	if edits == nil || plans == nil {
		t.Fatal("没有会话时 focusedStores 返回了 nil")
	}
	if got, id := a.focusedPlansAndSession(); got == nil || id != "" {
		t.Fatalf("没有会话时 focusedPlansAndSession = %v / %q，应为非 nil 存储 + 空 id", got, id)
	}

	// 有会话时给的是这个会话自己的那份。
	e := focusOn(a, "s1", t.TempDir())
	mine := newPlanStore()
	a.mu.Lock()
	e.plans = mine
	a.mu.Unlock()
	if _, plans = a.focusedStores(); plans != mine {
		t.Fatal("有会话时 focusedStores 没有给出该会话的计划存储")
	}
}

// TestCommandsAddressTheGivenSession 盯住显式寻址:命令打给哪条会话就落在哪条,
// 与"当前聚焦的是谁"无关。
//
// 这是并行下最容易出、也最难查的一类错误:B 会话弹出的授权按"当前"去解,用户在
// A 会话点的允许就解到 B 头上;B 的「已编辑」卡片读到的是 A 的记录。改动前所有
// 命令都隐式走 currentID,这类错误连报错都不会有。
func TestCommandsAddressTheGivenSession(t *testing.T) {
	a := New(&recordingSink{})
	other := focusOn(a, "other", t.TempDir())
	otherEdits, otherPlans := newEditStore(), newPlanStore()
	focused := focusOn(a, "focused", t.TempDir()) // 后调用的成为聚焦会话
	a.mu.Lock()
	other.edits, other.plans = otherEdits, otherPlans
	a.mu.Unlock()

	if a.focusedSessionID() != "focused" {
		t.Fatalf("聚焦的应该是 focused，实际 %q", a.focusedSessionID())
	}

	// 空串 = 聚焦会话（界面上大部分动作的写法，行为与改动前一致）。
	if e, err := a.entryOf(""); err != nil || e != focused {
		t.Fatalf("空串应解析到聚焦会话，得到 %v / %v", e, err)
	}
	// 显式 id = 那一条，哪怕它不是聚焦的那条。
	if e, err := a.entryOf("other"); err != nil || e != other {
		t.Fatalf("显式 id 没解析到对应会话，得到 %v / %v", e, err)
	}
	if id, err := a.sessionIDOf("other"); err != nil || id != "other" {
		t.Fatalf("sessionIDOf(other) = %q / %v", id, err)
	}
	// 不认识的 id 一律 errNoSession——不能悄悄退回聚焦会话，那正是要消灭的行为。
	if _, err := a.entryOf("nope"); err == nil {
		t.Fatal("未知会话 id 必须报错，绝不能退回聚焦会话")
	}

	// 存储也按 id 取：这条错了就是 B 的「已编辑」卡片显示 A 的记录。
	if got := a.editStoreOf("other"); got != otherEdits {
		t.Fatal("editStoreOf 取到的不是指定会话的编辑存储")
	}
	if got := a.editStoreOf(""); got != focused.edits {
		t.Fatal("空串应取聚焦会话的编辑存储")
	}
	if got, id := a.plansAndSessionOf("other"); got != otherPlans || id != "other" {
		t.Fatalf("plansAndSessionOf(other) = %v / %q", got, id)
	}

	// 会话关掉之后，按 id 寻址同样要报错（引擎那边已经没有它了）。
	a.mu.Lock()
	other.closed = true
	a.mu.Unlock()
	if _, err := a.entryOf("other"); err == nil {
		t.Fatal("已关闭的会话必须报 errNoSession")
	}
}
