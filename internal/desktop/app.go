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

	"github.com/wt68/runcode/tools/preview"
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
	// currentID is the active session's id ("" = none); the single-session
	// desktop routes every session-addressed command through it.
	currentID string
	// turnActive mirrors whether the active session has a turn in flight
	// (set on submit, cleared when the sink adapter observes turn:end /
	// turn:error). Only SwitchModel's mid-turn rebuild guard reads it.
	turnActive bool
	// lastUserText is the active session's most recent user message, feeding
	// the post-turn auto-title hook.
	lastUserText string
	// emit is the active session's envelope emitter (host-owned sequence);
	// pendingEmit is the one Configure captured for a Create still in flight.
	emit        func(event string, payload any)
	pendingEmit func(event string, payload any)
	// workspace is the directory of the active session, used to list/resume the
	// workspace's saved sessions. config is the configuration for the next session;
	// normally it matches the live session, but Settings may change it without
	// rebuilding that session. configPassport records its connection origin
	// explicitly so a Bridge-looking custom URL is never treated as Passport.
	workspace      string
	config         engine.Config
	configPassport bool
	liveConfig     engine.Config
	// livePassport/livePassportTenant describe the manager's currently open
	// connection. They stay unchanged when Settings selects a tenant for the next
	// session, keeping the in-chat model catalog aligned with actual request routing.
	livePassport       bool
	livePassportTenant string
	// preview is the loopback static server for the active workspace, and
	// previewURL its base URL (see startPreview/stopPreview in preview.go).
	preview    *previewServer
	previewURL string
	// edits captures Write/Edit pre/post content for the "已编辑" cards'
	// undo/review. One store per session (created in configureSession, promoted
	// on Create success); pendingEdits parks the store while its Create is in
	// flight. Never nil: before the first session it is an unbound store.
	edits        *editStore
	pendingEdits *editStore
	// tokens holds the Passport OAuth tokens (memory + DPAPI persistence); it is
	// also the engine's TokenSource. passportUser caches /api/me for PassportStatus.
	// loginCancel cancels an in-flight browser login wait.
	tokens       *tokenManager
	passportUser *PassportStatus
	loginCancel  context.CancelFunc
	// passportTenant is the tenant selected for the active passport session, so the
	// in-chat model picker can list that tenant's models.
	passportTenant string
}

// New returns an App that emits events to sink. Session events are enveloped
// and sequenced by the host (per-session seq space); process-level events are
// enveloped by the App's own sink with an empty session id.
func New(sink EventSink) *App {
	a := &App{
		out:            sink,
		sink:           newEnvelopeSink(sink),
		edits:          newEditStore(),
		passportTenant: strings.TrimSpace(loadRawConfig().TenantID),
	}
	a.mgr = host.NewManager(host.Options{
		Build:     host.DefaultBuild,
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
	return a
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
	s.app.out.Emit(env.Event, env)
}

// noteTurnDone clears the in-flight mirror when the active session's turn
// finishes (successfully or not).
func (a *App) noteTurnDone(sessionID string) {
	a.mu.Lock()
	if a.currentID == sessionID {
		a.turnActive = false
	}
	a.mu.Unlock()
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
		Mode:              cfg.PermissionMode,
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
			Breaker: permissions.NewHarmBreaker(0),
			Audit:   harmAuditFunc(sctx.Emit),
		},
	})
	opts.ExtraTools = append(opts.ExtraTools, preview.New())

	// Fresh edit store per session ("已编辑" undo/review), bound to the
	// session's edit directory before the first tool can run.
	edits := newEditStore()
	edits.BeginSession(cfg.CWD, sctx.ID)
	opts.EditRecorder = edits
	a.mu.Lock()
	a.pendingEdits = edits
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

// openSessionWithConnectionHeld is openSessionHeld with explicit connection
// identity. Callers that build a new connection pass its origin directly; callers
// that reuse a.config use openSessionHeld and inherit the stored identity.
func (a *App) openSessionWithConnectionHeld(cfg engine.Config, passport bool, tenantID string) (SessionInfo, error) {
	a.closeCurrentHeld()

	// Refresh the web-tool proxy from the persisted setting so "生效于下个会话"
	// holds even when cfg is a reused a.config snapshot from an earlier build.
	cfg.WebProxy = loadRawConfig().WebProxy

	id, st, err := a.mgr.Create(context.Background(), cfg)
	a.mu.Lock()
	pendingEdits, pendingEmit := a.pendingEdits, a.pendingEmit
	a.pendingEdits, a.pendingEmit = nil, nil
	if err != nil {
		a.mu.Unlock()
		return SessionInfo{}, err
	}
	a.currentID = id
	a.workspace = cfg.CWD
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
	a.turnActive = false
	a.lastUserText = ""
	a.emit = pendingEmit
	if pendingEdits != nil {
		a.edits = pendingEdits
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
	id := a.currentID
	a.mu.Unlock()
	if id != "" {
		_ = a.mgr.Close(context.Background(), id)
	}
	a.mu.Lock()
	a.currentID = ""
	a.turnActive = false
	a.lastUserText = ""
	a.emit = nil
	a.mu.Unlock()
	a.stopPreview()
}

// currentSession resolves the active session handle through the manager.
func (a *App) currentSession() (host.Session, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return nil, errNoSession
	}
	return a.mgr.Session(id)
}

// engineSession returns the active session's full engine facade, for commands
// that need more than the host.Session slice (Compact, ToolList, MCPStatus,
// Reload*). The desktop always builds real sessions via host.DefaultBuild, so
// the assertion only fails if a test injects a fake — which then simply looks
// like "no session" to these commands.
func (a *App) engineSession() (*engine.Session, error) {
	s, err := a.currentSession()
	if err != nil {
		return nil, err
	}
	es, ok := s.(*engine.Session)
	if !ok {
		return nil, errNoSession
	}
	return es, nil
}

// emitSessionEvent publishes an event through the active session's envelope
// emitter (no-op without a session).
func (a *App) emitSessionEvent(event string, payload any) {
	a.mu.Lock()
	emit := a.emit
	a.mu.Unlock()
	if emit != nil {
		emit(event, payload)
	}
}

// Reset clears the in-memory working history (the on-disk log is untouched).
func (a *App) Reset() error {
	session, err := a.engineSession()
	if err != nil {
		return wireError(err)
	}
	session.ResetHistory()
	return nil
}

// Status returns the current session's display state.
func (a *App) Status() (SessionInfo, error) {
	session, err := a.currentSession()
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.sessionInfo(session.Status()), nil
}

// CloseSession ends the session and releases its resources.
func (a *App) CloseSession() error {
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.closeCurrentHeld()
	return nil
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
