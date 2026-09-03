package desktop

// app.go 是桌面外壳的骨架:App 结构本身、会话的开与关,以及各命令共用的错误包装
// 与状态映射。回合、自动标题、会话设置分别在 turn.go / title.go /
// session_settings.go;技能、子代理、MCP、通行证等按功能各自成文件。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/officetool"
	"github.com/wt68/runcode/internal/plantool"
	"github.com/wt68/runcode/internal/previewtool"
	"github.com/wt68/runcode/internal/skilltool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
)

// Sentinel errors for the desktop's own preconditions; wireError maps them to
// their protocol codes at every command boundary. Host-side failures already
// arrive as *protocol.Error and pass through untouched.
var (
	errNoSession = errors.New("no active session")
	errBusy      = errors.New("a turn is already in progress")
)

// Dialoger opens native file/folder pickers. The Wails shell implements it; the
// transport-agnostic core only needs the interface, so it stays testable.
type Dialoger interface {
	// PickFile opens a file-open dialog and returns the chosen path ("" if the user
	// cancelled).
	PickFile(title string) (string, error)
	// PickFolder opens a directory-open dialog and returns the chosen path ("" if the
	// user cancelled). defaultDir, when non-empty and existing, is the starting
	// directory.
	PickFolder(title, defaultDir string) (string, error)
	// PickImage opens an image-file dialog and returns the chosen path ("" if the
	// user cancelled).
	PickImage(title string) (string, error)
}

// Quitter 退出整个应用。与 Dialoger 同一个模式：退出是 Wails 那一侧的事，而
// internal/desktop 对 Wails 零依赖是整个分层的地基，所以在这里定接口、由外壳注入。
//
// 目前唯一的用途是版本更新——拉起安装器之后必须退出，否则 NSIS 覆盖不了正在运行
// 的 exe，单实例锁也还攥在手里（见 update.go 的 quitSoon）。
type Quitter interface{ Quit() }

// sessionEntry 是一个会话在**外壳侧**的全部状态。引擎侧那份在 host.Manager 的
// 会话表里,两边靠 id 关联。
//
// 字段分两类,并发规则不同:
//   - id / workspace / emit / edits / plans 在建条目时一次写定,之后只读。取到
//     条目指针后可以脱锁使用(emit 本来就必须在锁外调用——它会走到宿主的发射路径)。
//   - turnActive / lastUserText / closed 随回合与生命周期变化,**只能在持 App.mu
//     时读写**。条目自身不带锁:这几个字段的读写都是取值/赋值,没有一处需要在持锁
//     期间做慢操作。等到某个字段真需要长时间持有(比如以后每会话一条连接),再给
//     条目加锁,并遵守 startMu → App.mu → entry.mu 的次序。
type sessionEntry struct {
	id string
	// seq 是登记序号(打开的先后)。会话列表按它排——**不能**按 a.sessions 的遍历序:
	// Go 的 map 遍历是随机的,每次回读列表都会换一个顺序,界面上的行于是自己在跳。
	// 用户点一行、列表重排、再点同一个位置就点到了别人身上——这类"点错了会话"的
	// 事故没有任何报错,只有一次不可撤销的后果(比如关掉一条正在跑的会话)。
	seq       int64
	workspace string
	emit      func(event string, payload any)
	edits     *editStore
	plans     *planStore

	// turnActive mirrors whether this session has a turn in flight (set on
	// submit, cleared when the sink adapter observes turn:end / turn:error).
	turnActive bool
	// lastUserText is this session's most recent user message, feeding the
	// post-turn auto-title hook.
	lastUserText string
	// closed 记录引擎侧会话已经关闭。条目本身要留在表里——关闭后「已编辑」卡片
	// 仍然可复审,直到下一个会话开出来把它替换掉。
	closed bool
}

// App is the desktop shell: a thin adapter between the Wails bindings and the
// host session manager. The manager owns everything session-shaped — the
// session table, turn goroutines, tool-event pumps, async approvals, envelope
// sequencing — while App keeps only the desktop's own policy and state: the
// single-active-session rule (currentID), the workspace preview server, the
// per-session edit store, passport login, and the persisted settings. Command
// methods never block the UI thread: long work (a turn) runs on host-owned
// goroutines and reports back through events.
type App struct {
	// out is the shell's raw sink; host envelopes are forwarded to it verbatim
	// (hostSinkAdapter). sink wraps the same sink for process-level events
	// (passport:changed, ...) that belong to no session — see envelopeSink for
	// the two-seq-space contract.
	out    EventSink
	sink   EventSink
	dialog Dialoger
	// quit 由外壳注入（SetQuitter），只有版本更新用得上；没注入时更新装完不会
	// 自动退出，其余一切照常。
	quit Quitter

	// upd 是版本更新的状态机（见 update.go）。它自带锁，与 mu / startMu 没有嵌套
	// 关系——更新与对话是两条互不相干的线（同 rec）。始终非 nil。
	upd *updater

	// rec 是录音纪要的状态（一次只允许一场）。它自带锁，与 mu / startMu
	// 没有嵌套关系——录音与对话是两条互不相干的线。
	rec recorderCtl

	// mgr is the host session manager. The desktop configures it with no
	// resource limits and no idle reclamation (a visible conversation must
	// never be reaped under the user).
	mgr *host.Manager

	// startMu serializes session lifecycle and connection-setting transitions
	// (including SaveSettings and SetActiveTenant), preserving both the desktop's
	// single-session policy and agreement between persisted/next/live connection
	// state. No path may hold a.mu while waiting for startMu.
	startMu sync.Mutex

	mu sync.Mutex
	// sessions 是外壳侧的会话表,键是引擎的会话 id。引擎那边的会话状态住在
	// host.Manager 自己的表里;这里存的是外壳独有的东西——信封发射器、编辑存储、
	// 计划存储、回合记账——靠 id 与之关联。
	sessions map[string]*sessionEntry
	// sessionSeq 发放 sessionEntry.seq,只增不减(关掉再开不会复用序号)。
	sessionSeq int64
	// focused 是当前聚焦的会话 id(""=没有)。没带会话 id 的命令一律打给它。
	//
	// 它可以指向一个**已关闭**的条目:会话关掉之后「已编辑」卡片仍然可复审,直到
	// 下一个会话把它替换掉。这是既有行为,靠"条目留在表里、focused 不变"来保持。
	focused string
	// pendingEmit is the emitter Configure captured for a Create still in flight.
	pendingEmit func(event string, payload any)
	// idleEdits/idlePlans 是「还没有任何会话」时的兜底存储:不记录任何东西,也不
	// 认识任何编辑。有它们在,editStore() 这类读取路径永远不会拿到 nil。
	idleEdits *editStore
	idlePlans *planStore
	// workspace is the directory of the active session, used to list/resume the
	// workspace's saved sessions. config is the configuration for the next session;
	// normally it matches the live session, but Settings may change it without
	// rebuilding that session. configPassport records its connection origin
	// explicitly so a Bridge-looking custom URL is never treated as Passport.
	//
	// **config.CWD 不可信,一律用 configForWorkspace 取**:它是上一次建会话时的整份
	// 快照,多工作区并行之后目录这一栏会过期(见那个函数的说明)。
	workspace      string
	config         engine.Config
	configPassport bool
	liveConfig     engine.Config
	// livePassport/livePassportTenant describe the manager's currently open
	// connection. They stay unchanged when Settings selects a tenant for the next
	// session, keeping the in-chat model catalog aligned with actual request routing.
	livePassport       bool
	livePassportTenant string
	// previews 是工作区 → 预览服务器的表(见 preview.go)。按**工作区**共享而不是
	// 按会话:同一目录的多个会话用同一台服务器,引用计数归零才停。
	previews map[string]*previewRef
	// pendingEdits parks the session's edit store while its Create is in flight;
	// on success it is promoted onto the session's entry (see sessionEntry.edits).
	pendingEdits *editStore
	// pendingPlans parks the session's staged planning run (计划模式) while its
	// Create is in flight — the document plan_write fills stage by stage, the
	// approval gate, and the persisted copy that lets a pending approval survive a
	// restart. Same lifecycle as pendingEdits; promoted onto the entry on success.
	pendingPlans *planStore
	// tokens holds the Passport OAuth tokens (memory + DPAPI persistence); it is
	// also the engine's TokenSource. passportUser caches /api/me for PassportStatus.
	// loginCancel cancels an in-flight browser login wait.
	tokens       *tokenManager
	passportUser *PassportStatus
	loginCancel  context.CancelFunc
	// passportTenant is the tenant selected for the active passport session, so the
	// in-chat model picker can list that tenant's models.
	passportTenant string
	// marketSynced records that the platform MCP market was fetched successfully
	// this run, so the sync happens once (startup or first login) instead of on
	// every session open. Guarded by mu.
	marketSynced bool
	// skillMarket/skillMarketAt 缓存技能市场的清单（见 skillmarket.go）。上游按它
	// 自己的节奏变，不必每次开页面都跑一趟网络；「已安装」标记不进缓存，那是本地事实。
	skillMarket   []marketSkillWire
	skillMarketAt time.Time
	// audit is the 上下文审核 runtime (store + viewer server + atomic switch),
	// active only in test builds; see contextaudit.go. Always non-nil.
	audit *contextAuditManager
}

// New returns an App that emits events to sink. Session events are enveloped
// and sequenced by the host (per-session seq space); process-level events are
// enveloped by the App's own sink with an empty session id.
func New(sink EventSink) *App { return newWithBuild(sink, host.DefaultBuild) }

// newWithBuild is New with an injectable session builder.
//
// 生产上只有一个 build(host.DefaultBuild,即真的去连模型)。留这个口子是为了能测
// **多会话并行**:那需要两条真的引擎会话同时在跑,而真会话要一个连得上的 provider。
// 引擎的 host.BuildFunc 本来就写着"tests inject a fake",这里只是把它透出来。
func newWithBuild(sink EventSink, build host.BuildFunc) *App {
	a := &App{
		out:            sink,
		sink:           newEnvelopeSink(sink),
		sessions:       map[string]*sessionEntry{},
		idleEdits:      newEditStore(),
		idlePlans:      newPlanStore(),
		passportTenant: strings.TrimSpace(loadRawConfig().TenantID),
		audit:          newContextAuditManager(),
	}
	a.mgr = host.NewManager(host.Options{
		Build:     build,
		Sink:      hostSinkAdapter{app: a},
		Limits:    host.Limits{}, // single-user shell: unbounded, IdleTimeout 0
		Configure: a.configureSession,
		OnTurnEnd: a.onTurnEnd,
	})
	pc := passportConfig()
	a.tokens = newTokenManager(pc.tokenURL(), pc.ClientID, passportHTTP(), func() {
		a.mu.Lock()
		a.passportUser = nil
		a.mu.Unlock()
		a.sink.Emit(EventPassportChanged, PassportStatus{})
	})
	a.tokens.loadPersisted()
	// 更新器每次状态变化整份发给前端（前端是它的镜子，理由见 internal/protocol/update.go）。
	// 构造放在 update.go：本文件的 protocol 是**引擎**那个包，而 UpdateInfo 是外壳自己的。
	a.upd = newUpdaterFor(a)
	return a
}

// Startup runs the app's once-per-run background work; the Wails shell calls it
// from OnStartup. It is deliberately not part of New so constructing an App
// (tests, tooling) performs no I/O.
func (a *App) Startup() {
	// 内置技能落盘。同步做而不是丢进 goroutine：它就是几个文件的本地写入（没有网络），
	// 而紧接着起的第一条会话要按目录去加载技能——异步的话首场会话有可能恰好赶在写完
	// 之前，那种"第一次用没有、重开一次就有了"的现象最难查。
	if wrote := installBuiltinSkills(); len(wrote) > 0 {
		debugLog("builtin skills installed: %v", wrote)
	}
	// Refresh the platform's MCP market once per run, off the session path: it
	// decides which servers carry the user's identity, and opening a session must
	// not pay a network round-trip to find out. A cold start with no stored login
	// is a no-op — PassportLogin syncs once the token arrives.
	go a.syncMarketOnce()
	// 先结算上一次更新装没装成（同步、纯本地文件读写），再起自动检查——反过来的话
	// 检查回来的状态会把"上次没装上"的说明冲掉。
	a.reportLastInstall()
	// 版本更新的自动检查。延后几秒再跑（见 updateCheckDelay）：它的结论最快也要等
	// 用户走到设置页才会被看见，没有理由和建窗口、装内置技能抢那几秒。
	go a.autoCheckUpdate(updateCheckDelay)
	// 上下文审核开关跨重启保持:测试版且上次开着,则恢复运行态(建目录、起查看
	// 服务器)。失败只记诊断日志——设置页再开一次会把错误如实报出来。
	if IsTestBuild() && loadRawConfig().ContextAudit {
		if _, err := a.audit.enable(); err != nil {
			debugLog("context audit restore: %v", err)
		}
	}
}

// hostSinkAdapter forwards host envelopes to the shell sink under the
// (event name, envelope) shape the frontend has always consumed. On the way
// through it lets the App observe turn completion (both turn:end and
// turn:error) to keep its turnActive mirror honest. Emit runs under the host's
// per-session emit lock, so neither path may block.
type hostSinkAdapter struct{ app *App }

func (s hostSinkAdapter) Emit(env protocol.Envelope) {
	switch env.Event {
	case EventTurnEnd, EventTurnError:
		s.app.noteTurnDone(env.SessionID)
	}
	// Record the turn lifecycle to the diagnostic log before forwarding, so a turn
	// that fails or ends empty is traceable even when nothing renders in the UI.
	logEnvelope(env)
	s.app.out.Emit(env.Event, env)
}

// noteTurnDone clears the in-flight mirror when a session's turn finishes
// (successfully or not). It keys off the envelope's session id rather than the
// focused session, so a background session's turn is recorded correctly too.
func (a *App) noteTurnDone(sessionID string) {
	a.mu.Lock()
	if e := a.entryLocked(sessionID); e != nil {
		e.turnActive = false
	}
	a.mu.Unlock()
}

// ---- 会话表的读写口 ---------------------------------------------------------
//
// 条目可变字段(turnActive / lastUserText / closed)的读写全部收在这一节,别在
// 别处直接摸。只读字段(id/workspace/emit/edits/plans)取到条目后可以脱锁用。

// entryLocked 取一个条目(可能已关闭)。调用方持 a.mu。
func (a *App) entryLocked(id string) *sessionEntry {
	if id == "" {
		return nil
	}
	return a.sessions[id]
}

// configForWorkspace 取"下一条会话"的配置快照,并把工作区**强制**成 ws
// (ws 为空时用聚焦会话的工作区)。开会话的路径一律经它拿 config,不要直接读。
//
// a.config 是进程级的"下一条会话用什么连接";它带着 CWD,只因为它是上一次建会话时
// 的整份快照。多工作区并行之后这一栏会过期——在 dir2 开过一条会话,a.config.CWD
// 就成了 dir2,而聚焦此刻可能已经切回 dir1 的那条。
//
// 照抄它的后果不是报错,是**在错的目录里开会话**:从 dir1 的「最近对话」点一条,
// 会拿着 dir1 的会话 id 去 dir2 的存储里恢复——那里没有这条记录,引擎于是照这个 id
// 在 dir2 建一条**空**对话(还会在那边落一个同名文件),而 dir1 那条正跑着的会话
// 已经被替换式打开关掉了。用户看到的是"点了最近对话,内容空了,原来那条也没了"。
//
// 所以:谁开会话,谁把目录说清楚。
func (a *App) configForWorkspace(ws string) engine.Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	cfg := a.config
	if strings.TrimSpace(ws) == "" {
		ws = a.workspace
	}
	cfg.CWD = ws
	return cfg
}

// workspaceOfSession 取一条会话记着的目录;不认识这条会话时回落到聚焦的工作区。
//
// 目录是**会话的属性**,不是 config/liveConfig 的——原地重建一条会话(换模型、
// 改设置、重载 MCP)必须重建在它自己的目录里,否则会话会在重建的一瞬间搬家。
func (a *App) workspaceOfSession(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.entryLocked(strings.TrimSpace(id)); e != nil && e.workspace != "" {
		return e.workspace
	}
	return a.workspace
}

// liveEntryLocked 取聚焦**且仍然存活**的条目,没有则 nil。调用方持 a.mu。
//
// "没有存活会话"正是各命令原先用 currentID == "" 判断的那件事——关闭时 currentID
// 被清空,而现在条目会留下来(为了「已编辑」的复审),所以判据变成了 closed。
func (a *App) liveEntryLocked() *sessionEntry {
	e := a.entryLocked(a.focused)
	if e == nil || e.closed {
		return nil
	}
	return e
}

// liveEntry 是 liveEntryLocked 的自持锁版本,没有存活会话时给 errNoSession。
func (a *App) liveEntry() (*sessionEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.liveEntryLocked()
	if e == nil {
		return nil, errNoSession
	}
	return e, nil
}

// entryOf 解析一条命令要作用在哪个会话上。
//
// 命令面上的 sessionID 是**显式寻址**:并行之后"当前是哪条"不再够用——B 会话弹
// 出的授权若按"当前"去解,用户在 A 会话点的允许就会解到 B 头上。
//
// 空串保留"聚焦会话"的含义:界面上大部分动作作用于用户正看着的那条,让每个调用
// 点自己去查一遍 id 只是噪音。会话不存在或已关闭一律 errNoSession——与改动前
// `id == ""` 时的行为一致。
func (a *App) entryOf(sessionID string) (*sessionEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := strings.TrimSpace(sessionID)
	if id == "" {
		if e := a.liveEntryLocked(); e != nil {
			return e, nil
		}
		return nil, errNoSession
	}
	e := a.entryLocked(id)
	if e == nil || e.closed {
		return nil, errNoSession
	}
	return e, nil
}

// sessionIDOf 是 entryOf 最常见的用法:只要 id。
func (a *App) sessionIDOf(sessionID string) (string, error) {
	e, err := a.entryOf(sessionID)
	if err != nil {
		return "", err
	}
	return e.id, nil
}

// storesOf 取指定会话的编辑与计划存储。会话不存在时给兜底存储 + errNoSession,
// 调用方可以按需要选择报错还是拿空存储继续。
func (a *App) storesOf(sessionID string) (*editStore, *planStore, error) {
	e, err := a.entryOf(sessionID)
	if err != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		// 关闭后的会话仍要能复审「已编辑」,所以这里退回聚焦条目(可能已关闭)的
		// 存储,再退回兜底存储——与 focusedStores 同一套规则。
		if sessionID == "" {
			if fe := a.entryLocked(a.focused); fe != nil {
				return fe.edits, fe.plans, nil
			}
		}
		return a.idleEdits, a.idlePlans, err
	}
	return e.edits, e.plans, nil
}

// liveSessionIDOrEmpty 返回聚焦且存活的会话 id,没有则空串。
// 它保留了各命令原先 `id := a.currentID` + `if id == ""` 那种写法的形状。
func (a *App) liveSessionIDOrEmpty() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.liveEntryLocked(); e != nil {
		return e.id
	}
	return ""
}

// plansAndSessionOf 返回指定会话(空串 = 聚焦会话)的计划存储与 id;没有存活会话时
// id 为空串,存储给兜底的那个(非 nil,调用方照旧可以判 id 来决定报不报错)。
func (a *App) plansAndSessionOf(sessionID string) (*planStore, string) {
	e, err := a.entryOf(sessionID)
	if err != nil {
		return a.idlePlans, ""
	}
	return e.plans, e.id
}

// focusedPlansAndSession 是 plansAndSessionOf 作用于聚焦会话的写法。
func (a *App) focusedPlansAndSession() (*planStore, string) { return a.plansAndSessionOf("") }

// focusedSessionID 返回聚焦会话的 id,**包括已关闭的**;没有会话时是空串。
// 给"只是想知道当前指着谁"的地方用,比如判断一个回调是否还属于当前会话。
func (a *App) focusedSessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.focused
}

// turnInFlight 报告聚焦会话是否有回合在跑。
func (a *App) turnInFlight() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entryLocked(a.focused)
	return e != nil && e.turnActive
}

// focusedStores 返回聚焦会话的编辑与计划存储;没有会话时给兜底的空存储。
// 两者都保证非 nil。
func (a *App) focusedStores() (*editStore, *planStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entryLocked(a.focused)
	if e == nil {
		return a.idleEdits, a.idlePlans
	}
	return e.edits, e.plans
}

// wireError normalizes a command failure for the wire: a *protocol.Error (the
// host's command errors are such values) passes through unchanged, the
// desktop's sentinels map to their codes, and anything else becomes an
// internal error carrying the original message. Every exported command wraps
// its error returns with it, so the frontend always receives the structured
// form (protocol.Error.Error() serializes as JSON across the Wails bridge).
func wireError(err error) error {
	if err == nil {
		return nil
	}
	var pe *protocol.Error
	if errors.As(err, &pe) {
		return pe
	}
	switch {
	case errors.Is(err, errNoSession):
		return &protocol.Error{Code: protocol.ErrCodeNoSession, Message: err.Error()}
	case errors.Is(err, errBusy):
		return &protocol.Error{Code: protocol.ErrCodeBusy, Message: err.Error()}
	}
	return &protocol.Error{Code: protocol.ErrCodeInternal, Message: err.Error()}
}

// SetDialoger installs the native file-dialog provider (called by the shell).
func (a *App) SetDialoger(d Dialoger) { a.dialog = d }

// SetQuitter installs the app-quit provider (called by the shell).
func (a *App) SetQuitter(q Quitter) { a.quit = q }

// hostToolClasses is the permission classification of the three tools this shell
// registers itself. The engine's resolver keys off a fixed tool-name switch, and a
// name it does not know resolves to unknown/high-risk — which the default policy
// hard-denies. So whoever supplies a tool has to supply its class, and for these
// three that is us.
//
// All three are ClassReadOnly, which the engine resolves to side-effect-free
// management: allowed without approval, and past plan mode's mutation block.
//   - plan_write writes to this process's plan store and one file under .runcode,
//     recording the plan the user is about to approve — exactly TodoWrite's shape.
//   - open_preview only opens a panel in this window.
//   - ReadOffice reads one document and enforces workspace containment itself.
//
// It is stated here rather than left to the engine on purpose: two of these names
// still appear in the engine's own resolver switch, which is the engine knowing
// about desktop-only tools. Classifying them here is what lets that go away.
var hostToolClasses = map[string]permissions.ToolClass{
	plantool.Name:    permissions.ClassReadOnly,
	previewtool.Name: permissions.ClassReadOnly,
	officetool.Name:  permissions.ClassReadOnly,
}

// harmAutoAllowLimit 是智能模式一个会话里最多免弹窗放行多少次危险操作。
//
// 引擎的默认值是 50（permissions.defaultHarmAutoAllowLimit）；这里显式调到 1000，
// 因为 50 在真实的长会话里会中途耗尽，之后每一次命令都要人点一下，等于把智能模式
// 悄悄降级成交互模式——而用户往往不知道发生了什么，只觉得"它突然开始一直问"。
//
// 要清楚这个数在防什么：它防的不是用户，是**判定模型被骗或被攻破**。一个被诱导的
// 判定模型可以连着说一千次"安全"，熔断器保证到第 1001 次时人必须重新介入。调高它
// 是在削弱这层兜底，换取长会话不被打断——这是一个有意的取舍，不是"这个数没用"。
//
// 三道闸门并没有因此松动，它们与本上限无关、照常生效：
//   - 确定性底线（外部 MCP 调用、敏感文件改动、工作区外写入、特权/破坏性操作）
//     一律弹窗，判定模型说安全也不放行，且**不消耗**本额度；
//   - 判定失败/超时一律转为询问（fail-safe），同样不消耗额度；
//   - 每一次自动放行都经 harm:autoallow 事件落到界面上，工具卡可展开看判定理由。
//
// 放在外壳而不是改引擎默认值：这是客户端策略，不是引擎契约。改引擎要打 tag、升
// require，而 NewHarmBreaker(limit) 这个参数本来就是为宿主自定义留的。
const harmAutoAllowLimit = 1000

// configureSession is the host's per-session Configure hook — the desktop's
// contribution to each session's assembly (migrated from the pre-host
// buildAndSetLocked). It wires: (1) the interactive permission service around
// the host's async approver, with the model harm judge, a per-session harm
// breaker, and the audit feed emitting harm:autoallow through the session's
// emitter; (2) the open_preview extra tool; (3) a fresh per-session edit store
// as the EditRecorder (replacing the old app-wide store whose sessions
// overwrote each other). It runs on the Create path outside every host lock.
// The edit store and emitter are parked as pending and published to the App
// only after Create succeeds (openSessionHeld); a failed build leaves the
// previous session's wiring untouched.
func (a *App) configureSession(sctx host.SessionContext, cfg *engine.Config, opts *engine.Options) {
	store, err := engine.NewAllowStore(cfg.CWD)
	if err != nil {
		// A corrupt allow file must not block the session: degrade to an
		// in-memory store (project-scope allows won't persist) and say so.
		sctx.Emit(EventWarning, Warning{Message: fmt.Sprintf("权限允许清单不可用（本次会话仅内存记忆）: %v", err)})
		store = permissions.NewMemorySessionAllowStore()
	}
	// Always mode-aware (interactive authorizer installed even in safe mode) so
	// the UI can switch modes at runtime.
	opts.Permissions = permissions.NewService(permissions.Options{
		Mode: cfg.PermissionMode,
		// The tools this shell registers itself are this shell's to classify;
		// everything else falls through to the engine's resolver (see
		// hostToolClasses). engine.Options.ToolClasses is the same thing for hosts
		// that let the engine build the service — we build our own, so we install
		// the classifier ourselves.
		Resolver: permissions.WithToolClasses(nil, hostToolClasses),
		// Which servers we vouch for is ours to know, not the engine's: the same
		// opt-in that earns a server the user's identity headers also lets its calls
		// skip the per-call approval an arbitrary external endpoint always needs.
		// Both read the one name set (passportMCPNames), so they cannot disagree.
		//
		// Same shape for the app's own directories: which paths are "this
		// application" is host knowledge the engine cannot have, so the shell wraps
		// the default policy and stops the outside-workspace prompt for them alone
		// (see appdirs.go — data dirs read+write, install dir read-only).
		Policy:            newAppDirPolicy(permissions.DefaultPolicy{TrustedMCPServers: passportMCPNames()}),
		ApprovalAvailable: true,
		InteractiveAuthorizer: permissions.InteractiveAuthorizer{
			Approver: sctx.Approver,
			Store:    store,
			// Model harm gate: auto-allow actions the model judges safe; only
			// prompt for ones it flags as potentially harmful (or when the check
			// fails).
			HarmJudge: modelHarmJudge{app: a},
			// Session-scoped audit + circuit breaker: surface every smart-mode
			// auto-allow to the UI, and stop auto-allowing once the session
			// budget is spent so a fooled judge can't silently wave through an
			// unbounded stream.
			Breaker: permissions.NewHarmBreaker(harmAutoAllowLimit),
			Audit:   harmAuditFunc(sctx.Emit),
		},
	})
	// Fresh planning run per session (阶段化计划模式), bound before the first tool
	// can run so plan_write always has somewhere to write. It loads any plan left
	// on disk, so reopening a session lands back on its approval gate.
	plans := newPlanStore()
	plans.BeginSession(cfg.CWD, sctx.ID, sctx.Emit, a.planModeOn)

	// open_preview lets the model surface a produced file in the preview panel;
	// ReadOffice lets it read .docx/.xlsx/.pptx as structured text (fonts,
	// formatting, layout) instead of the raw ZIP bytes plain Read would dump;
	// plan_write records plan mode's staged output as an approvable checklist.
	// ReadOffice 额外收下本应用自己的可读目录:粘贴进输入框的附件落在那儿(见
	// attachments.go 的 SavePastedFile),而 Office 文件除了 ReadOffice 没别的读法——
	// Read 对它只会吐二进制垃圾。根的口径与 appDirPolicy 放行读的那份完全一致。
	opts.ExtraTools = append(opts.ExtraTools, previewtool.New(), officetool.New(appDirsOnce().readRoots...), plantool.New(plans))
	// The desktop's Skill tool discloses exactly what the engine's does and
	// additionally announces each load, so the chat can show which skill the model
	// picked up and what it is for. Which skills exist stays the engine's business
	// (discovery, precedence, the disabled list, ReloadSkills).
	opts.SkillTool = skilltool.New()

	// 上下文审核观测器只在测试版接线;正式版连回调都不装,功能整体不存在。
	// 回调内部查原子开关,所以运行中切换立即对当前会话生效,无需重建。
	if IsTestBuild() {
		opts.LLMRequestObserver = a.audit.observer(sctx.ID)
	}

	// Fresh edit store per session ("已编辑" undo/review), bound to the
	// session's edit directory before the first tool can run.
	edits := newEditStore()
	edits.BeginSession(cfg.CWD, sctx.ID)
	opts.EditRecorder = edits
	a.mu.Lock()
	a.pendingEdits = edits
	a.pendingPlans = plans
	a.pendingEmit = sctx.Emit
	a.mu.Unlock()
}

// StartSession opens (or reopens) the session for a workspace and returns its
// display state. Any existing session is closed first.
func (a *App) StartSession(req StartSessionRequest) (SessionInfo, error) {
	originalReq := req
	var err error
	req, err = a.resolveCustomModelRequest(req)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	cfg, err := buildConfig(req)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	cfg = a.applyPassport(cfg, req)
	if strings.EqualFold(strings.TrimSpace(req.Provider), "passport") && !a.tokens.LoggedIn() {
		return SessionInfo{}, wireError(errors.New("未登录通行证，请先登录后再选择平台模型"))
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	// Record the workspace before the build so session listing works even when
	// this start fails (pre-host behavior).
	a.mu.Lock()
	a.workspace = cfg.CWD
	a.mu.Unlock()
	isPassport := strings.EqualFold(strings.TrimSpace(req.Provider), "passport")
	info, err := a.openSessionWithConnectionHeld(cfg, isPassport, req.TenantID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Persist only the profile reference, never the resolved API key/Base URL copy.
	saveConfig(customModelPersistenceRequest(originalReq, req))
	return info, nil
}

// openSessionHeld closes the active session (if any) and creates a new one
// from cfg — the desktop's "one active session; switching closes the old one
// first" policy. The caller must hold startMu; no lock is held across the
// manager calls (Configure re-enters a.mu). On success the new session's
// id/config/edit store/emitter are recorded and the workspace preview server
// restarted; on failure the previous session is already closed (matching the
// pre-host buildAndSetLocked) and the recorded config stays unchanged.
func (a *App) openSessionHeld(cfg engine.Config) (SessionInfo, error) {
	return a.openSessionWithConnectionHeld(cfg, a.configPassport, a.passportTenant)
}

// addSessionHeld 打开一条会话并聚焦它,**不关掉任何已经开着的会话**——"加开一条"。
// 要恢复的那条已经开着就直接聚焦过去。调用方须持 startMu。
//
// 它与 openSessionWithConnectionHeld 只差一句 closeCurrentHeld,而那一句就是
// "替换式打开"的全部含义。界面上所有"打开一条会话"的入口(新建对话、换工作区、
// 点开一条历史对话)走的都是这一条:**没有任何一个动作会顺手销毁别的会话**,
// 关闭只有 CloseSession 一个明确入口。
//
// 这条规矩是被同一个 bug 反复教出来的:三次报障都是"我发出去的活正跑着,点了个
// 别的东西,它就没了"——先是新建对话,再是换工作区,最后是点最近对话。替换式打开
// 留给真正意味着"这条会话要被换掉"的地方:换模型/改设置的原地重建、重载 MCP。
func (a *App) addSessionHeld(cfg engine.Config, passport bool, tenantID string) (SessionInfo, error) {
	if info, ok, err := a.focusIfAlreadyOpenHeld(cfg.Resume); ok {
		return info, err
	}
	return a.buildSessionHeld(cfg, passport, tenantID)
}

// openSessionWithConnectionHeld is openSessionHeld with explicit connection
// identity. Callers that build a new connection pass its origin directly; callers
// that reuse a.config use openSessionHeld and inherit the stored identity.
func (a *App) openSessionWithConnectionHeld(cfg engine.Config, passport bool, tenantID string) (SessionInfo, error) {
	if info, ok, err := a.focusIfAlreadyOpenHeld(cfg.Resume); ok {
		return info, err
	}
	a.closeCurrentHeld()
	return a.buildSessionHeld(cfg, passport, tenantID)
}

// focusIfAlreadyOpenHeld 拦下「要恢复的这条会话已经开着，而且不是聚焦的那条」，
// 直接聚焦过去;ok=true 表示本次打开到此为止,不再关旧的、也不再重建。
// 调用方须持 startMu。
//
// 不拦会怎样:替换式打开先关掉**聚焦**的那条,再拿目标 id 去 Manager.Create——
// 而目标此刻仍在 Manager 的表里,于是撞上 host.ErrSessionExists("session already
// exists")。用户看到的是:点了侧栏「最近对话」里某条正开着的会话,报一句莫名其妙的
// 英文错误,而且刚才聚焦的那条已经被关掉了——白丢一条会话,目标也没打开。
// 多会话之前这不可能发生(同时只有一条会话开着,目标要么就是聚焦的那条、要么没开)。
//
// 为什么"是聚焦的那条"要放行:那是**原地重建**——换模型、改设置、重载 MCP 都靠
// "关掉再按同一个 id 恢复"来让新配置生效,同时保住对话历史(见 mcp.go 的 ReloadMCP
// 与 session_settings.go 的 rebuildResumingWithConnectionHeld)。那条路必须照旧。
//
// 代价:目标已开着时,本次调用带来的 cfg 变更(比如起始页选了别的模型)不会生效——
// 要按新配置重开,得先关掉那条会话。这比"报错并连坐关掉聚焦的那条"好得多,
// 而且换模型有 SetModel 那条按会话生效的正路。
func (a *App) focusIfAlreadyOpenHeld(resume string) (SessionInfo, bool, error) {
	id := strings.TrimSpace(resume)
	if id == "" {
		return SessionInfo{}, false, nil
	}
	a.mu.Lock()
	if id == a.focused {
		a.mu.Unlock()
		return SessionInfo{}, false, nil // 原地重建,照旧走关闭再建
	}
	e := a.entryLocked(id)
	if e == nil || e.closed {
		a.mu.Unlock()
		return SessionInfo{}, false, nil // 没开着(或只剩个供复审的死条目),正常恢复
	}
	a.focusLocked(e)
	a.mu.Unlock()
	// 与 FocusSession 同一套动作:切聚焦(顺带切工作区)后回读状态。
	info, err := a.Status(id)
	return info, true, err
}

// buildSessionHeld 建一条会话并登记进会话表,**不动已经开着的会话**。
//
// "先关掉当前那条"是调用方的策略(替换式打开:StartSession / NewSession /
// ResumeSession 都是这个语义),不是"开一条会话"本身的一部分。多会话要的正是
// 不带那道策略的这一半。调用方须持 startMu。
func (a *App) buildSessionHeld(cfg engine.Config, passport bool, tenantID string) (SessionInfo, error) {
	// Refresh the web-tool proxy from the persisted setting so "生效于下个会话"
	// holds even when cfg is a reused a.config snapshot from an earlier build.
	cfg.WebProxy = loadRawConfig().WebProxy
	// Same for the MCP servers: New/Resume/Open all reuse the a.config
	// snapshot taken at startup, so without this an installed or edited server
	// would not connect until the app was restarted — contradicting the MCP page's
	// "更改在下次新建会话时生效". Re-reading here (the one place every session is
	// opened) makes that promise true on all paths, and re-attaches the Passport
	// identity to the servers that the platform market marked as its own.
	//
	cfg.MCPServers, cfg.AllowMCPSampling = loadDesktopMCP(cfg.CWD)
	a.attachMCPPassport(cfg.MCPServers)

	id, st, err := a.mgr.Create(context.Background(), cfg)
	a.mu.Lock()
	pendingEdits, pendingPlans, pendingEmit := a.pendingEdits, a.pendingPlans, a.pendingEmit
	a.pendingEdits, a.pendingPlans, a.pendingEmit = nil, nil, nil
	if err != nil {
		a.mu.Unlock()
		return SessionInfo{}, err
	}
	// 上一个会话的条目在这里退场:「关闭后仍可复审」到此为止——既有注释里那句
	// "until a new session replaces them",替换点就是这里。
	a.dropClosedLocked()
	// 登记顺带把 focused 与 workspace 一起切到新会话(见 focusLocked)。
	a.registerSessionLocked(id, cfg.CWD, pendingEdits, pendingPlans, pendingEmit)
	a.config = cfg
	a.configPassport = passport
	a.liveConfig = cfg
	a.livePassport = passport
	if passport {
		a.passportTenant = strings.TrimSpace(tenantID)
		a.livePassportTenant = strings.TrimSpace(tenantID)
	} else {
		a.livePassportTenant = ""
	}
	a.mu.Unlock()

	// Restart the workspace preview server for this session (non-fatal on error).
	a.startPreview(cfg.CWD)
	return a.sessionInfo(st), nil
}

// closeCurrentHeld tears down the active session — the host cancels its turn,
// denies pending approvals, stops the tool-event pump, and closes the engine
// session — then stops the preview server. The caller must hold startMu; safe
// with no session. The edit store handle survives so a closed session's
// "已编辑" records stay reviewable until a new session replaces them
// (pre-host behavior).
func (a *App) closeCurrentHeld() {
	a.mu.Lock()
	e := a.entryLocked(a.focused)
	a.mu.Unlock()
	// 留着条目:替换式打开时界面上那条对话还在,「已编辑」卡片要能继续复审,
	// 直到新会话开出来(buildSessionHeld 里的 dropClosedLocked)把它换掉。
	a.closeEntryHeld(e, false)
}

// closeEntryHeld 关掉一条会话:引擎那边取消回合、拒掉挂起的授权、关会话,
// 然后释放它那个工作区的预览引用。调用方须持 startMu;e 为 nil 时是 no-op。
//
// drop 决定条目本身留不留:
//   - false —— 替换式打开(关掉当前那条,马上开一条新的)。界面上旧对话还在,
//     「已编辑」卡片要能继续复审,所以条目留着、focused 也不动。
//   - true —— 用户主动关掉这条会话。它从界面上整个消失了,留着只是占内存,
//     还会让"打开中的会话"列表多出一条已经死掉的。
func (a *App) closeEntryHeld(e *sessionEntry, drop bool) {
	if e == nil {
		return
	}
	a.mu.Lock()
	id := ""
	if !e.closed {
		id = e.id
	}
	a.mu.Unlock()
	if id != "" {
		_ = a.mgr.Close(context.Background(), id)
	}
	a.mu.Lock()
	e.closed = true
	e.turnActive = false
	e.lastUserText = ""
	e.emit = nil
	if drop {
		delete(a.sessions, e.id)
		if a.focused == e.id {
			// 还开着别的会话就顺势聚焦其中一条,免得界面落到"没有会话"的空态。
			var next *sessionEntry
			for _, other := range a.sessions {
				if !other.closed {
					next = other
					break
				}
			}
			a.focusLocked(next)
		}
	}
	a.mu.Unlock()
	a.stopPreview(previewWorkspaceOf(e))
}

// registerSessionLocked 把 Create 成功的会话登记进会话表并聚焦它。调用方持 a.mu。
//
// 它**不关掉别的会话**——那是调用方的策略(当前只允许一条活动会话,所以开新会话
// 前先 closeCurrentHeld)。拆出来是为了让开一条会话与关掉旧的这两件事分开:
// 多会话 UI(P2)要的正是前者不带后者。
func (a *App) registerSessionLocked(id, workspace string, edits *editStore, plans *planStore, emit func(string, any)) {
	a.sessionSeq++
	entry := &sessionEntry{id: id, seq: a.sessionSeq, workspace: workspace, emit: emit, edits: edits, plans: plans}
	// Configure 一定在 Create 成功前跑过,所以这两个不该为 nil;真为 nil 时用兜底
	// 存储顶上,免得后面每个取用点都要判空。
	if entry.edits == nil {
		entry.edits = a.idleEdits
	}
	if entry.plans == nil {
		entry.plans = a.idlePlans
	}
	a.sessions[id] = entry
	a.focusLocked(entry)
}

// focusLocked 把聚焦切到 e,并把工作区一并搬过去。调用方持 a.mu;e 为 nil 表示
// 没有会话可聚焦。
//
// 收在一处是因为这两个字段必须同进同退:没带会话 id 的命令打给 focused,而工作区
// 寻址的那批命令(文件列表、技能、子代理、MCP、记忆、会话列表)读的是 workspace。
// 两者错开的表现是切了会话,文件浏览器还停在上一条的目录上——不报错,只是不对。
func (a *App) focusLocked(e *sessionEntry) {
	if e == nil {
		a.focused = ""
		return
	}
	a.focused = e.id
	a.workspace = e.workspace
}

// dropClosedLocked 清掉表里已关闭的条目。调用方持 a.mu。
//
// 只在新会话开出来时调用:关闭本身不删条目,那样「已编辑」卡片会当场失去数据源。
func (a *App) dropClosedLocked() {
	for id, e := range a.sessions {
		if e.closed {
			delete(a.sessions, id)
		}
	}
}

// previewWorkspaceOf 取条目的工作区,条目为 nil 时返回空串(没有预览要停)。
func previewWorkspaceOf(e *sessionEntry) string {
	if e == nil {
		return ""
	}
	return e.workspace
}

// sessionHandleOf resolves a session's handle through the manager
// (空串 = 聚焦会话,见 entryOf)。
func (a *App) sessionHandleOf(sessionID string) (host.Session, error) {
	id, err := a.sessionIDOf(sessionID)
	if err != nil {
		return nil, err
	}
	return a.mgr.Session(id)
}

// currentSession resolves the focused session's handle through the manager.
func (a *App) currentSession() (host.Session, error) { return a.sessionHandleOf("") }

// engineSession returns the active session's full engine facade, for commands
// that need more than the host.Session slice (Compact, ToolList, MCPStatus,
// Reload*). The desktop always builds real sessions via host.DefaultBuild, so
// the assertion only fails if a test injects a fake — which then simply looks
// like "no session" to these commands.
func (a *App) engineSessionOf(sessionID string) (*engine.Session, error) {
	s, err := a.sessionHandleOf(sessionID)
	if err != nil {
		return nil, err
	}
	es, ok := s.(*engine.Session)
	if !ok {
		return nil, errNoSession
	}
	return es, nil
}

// engineSession is engineSessionOf for the focused session.
func (a *App) engineSession() (*engine.Session, error) { return a.engineSessionOf("") }

// emitSessionEvent publishes an event through the active session's envelope
// emitter (no-op without a session).
func (a *App) emitSessionEvent(event string, payload any) {
	a.mu.Lock()
	var emit func(string, any)
	if e := a.entryLocked(a.focused); e != nil {
		emit = e.emit
	}
	a.mu.Unlock()
	if emit != nil {
		emit(event, payload)
	}
}

// Reset clears the in-memory working history (the on-disk log is untouched).
func (a *App) Reset(sessionID string) error {
	session, err := a.engineSessionOf(sessionID)
	if err != nil {
		return wireError(err)
	}
	session.ResetHistory()
	// A plan is a reading of a conversation that no longer exists; keeping it would
	// leave the approval board pinned over an empty chat.
	plans, _ := a.plansAndSessionOf(sessionID)
	plans.Clear()
	return nil
}

// Status returns the current session's display state.
func (a *App) Status(sessionID string) (SessionInfo, error) {
	session, err := a.sessionHandleOf(sessionID)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.sessionInfo(session.Status()), nil
}

// sessionInfo maps an engine status to the wire SessionInfo, attaching the
// live preview base URL.
func (a *App) sessionInfo(st engine.Status) SessionInfo {
	return SessionInfo{
		SessionID:          st.SessionID,
		Model:              st.Model,
		CWD:                st.CWD,
		PermissionMode:     st.PermissionMode,
		PlanMode:           st.PlanMode,
		ReasoningScenario:  st.ReasoningScenario,
		ThinkingEffort:     st.ThinkingEffort,
		MaxContextTokens:   st.MaxContextTokens,
		InputPricePerMTok:  st.InputPricePerMTok,
		OutputPricePerMTok: st.OutputPricePerMTok,
		PricingSource:      st.PricingSource,
		PreviewBaseURL:     a.previewBaseURL(),
	}
}

// GetProtocolInfo reports the wire-protocol version this host implements, so a
// frontend built against a different protocol can detect the mismatch instead
// of failing on a missing field. Same-package builds (Wails) never skew; the
// command exists so one frontend protocol layer works across transports.
func (a *App) GetProtocolInfo() protocol.Info {
	return protocol.Info{ProtocolVersion: protocol.Version}
}
