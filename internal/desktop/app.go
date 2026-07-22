package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/turn"
	"github.com/wt68/runcode/tools/preview"
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

	// startMu serializes session lifecycle transitions (StartSession /
	// NewSession / ResumeSession / SwitchWorkspace / SwitchModel-rebuild /
	// CloseSession), preserving the desktop's "close the old session, then open
	// the new one" atomicity. It is never held while a.mu is taken by another
	// path, and never across anything but mgr lifecycle calls.
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
	// workspace's saved sessions. config is the last-built configuration, reused
	// (with a different Resume/SessionID) to open another session in the same
	// workspace without re-collecting provider/model/credentials.
	workspace string
	config    engine.Config
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
	a := &App{out: sink, sink: newEnvelopeSink(sink), edits: newEditStore()}
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
	info, err := a.openSessionHeld(cfg)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Persist the form values so the next launch prefills them.
	saveConfig(req)
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

// SendMessage runs one user turn asynchronously. It returns immediately; the
// turn's result arrives as an EventTurnEnd or EventTurnError. It errors only when
// there is no session or a turn is already running.
func (a *App) SendMessage(text string) error {
	return wireError(a.sendUserTurn(text, nil, false))
}

// sendUserTurn submits one user turn to the active session via the manager,
// maintaining the desktop-side turn bookkeeping: the in-flight mirror, the
// auto-title text, and the per-turn edit baseline reset.
func (a *App) sendUserTurn(text string, images []llm.ImageSource, withImages bool) error {
	a.mu.Lock()
	id := a.currentID
	edits := a.edits
	a.mu.Unlock()
	if id == "" {
		return errNoSession
	}

	a.mu.Lock()
	prevText := a.lastUserText
	a.lastUserText = text
	a.turnActive = true
	a.mu.Unlock()

	var err error
	if withImages {
		err = a.mgr.SendMessageWithImages(id, text, images)
	} else {
		err = a.mgr.SendMessage(id, text)
	}
	if err != nil {
		a.mu.Lock()
		a.lastUserText = prevText
		// A busy rejection means another turn is still running; any other
		// failure means nothing is in flight.
		a.turnActive = errors.Is(err, host.ErrBusy)
		a.mu.Unlock()
		return err
	}
	// Reset the per-turn edit baselines. This runs just after the turn
	// goroutine launched but strictly before any tool can execute (the turn
	// first completes a model round-trip), so it is observably equivalent to
	// the pre-host "BeginTurn before RunTurn".
	edits.BeginTurn()
	return nil
}

// titleGenTimeout bounds the off-turn title-generation request so a slow or stuck
// model call cannot leak a goroutine.
const titleGenTimeout = 30 * time.Second

// titleGenerator is the slice of engine.Session auto-title needs (an interface
// so tests can fake the session through the host's BuildFunc).
type titleGenerator interface {
	GenerateTitle(ctx context.Context, userText string) (string, error)
}

// onTurnEnd is the host's post-turn hook (Options.OnTurnEnd): it fires on the
// turn goroutine right after the turn:end envelope. Per the hook's contract it
// must not block, so it only launches the title refresh goroutine.
func (a *App) onTurnEnd(sctx host.SessionContext, _ turn.Result) {
	a.mu.Lock()
	text := a.lastUserText
	current := a.currentID
	a.mu.Unlock()
	if sctx.ID != current || strings.TrimSpace(text) == "" {
		return
	}
	go a.refreshTitle(sctx, text)
}

// refreshTitle generates a short title for the session from userText, persists it
// to the session's sidecar, and announces it (session:renamed via the session's
// emitter) so the sidebar can update. It regenerates after every turn so the
// title tracks the latest request, and is best-effort: any error (model
// failure, closed session, write error) is ignored.
func (a *App) refreshTitle(sctx host.SessionContext, userText string) {
	s, err := a.mgr.Session(sctx.ID)
	if err != nil {
		return // session already closed or replaced
	}
	tg, ok := s.(titleGenerator)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), titleGenTimeout)
	defer cancel()
	title, err := tg.GenerateTitle(ctx, userText)
	if err != nil || strings.TrimSpace(title) == "" {
		return
	}
	a.mu.Lock()
	ws := a.workspace
	current := a.currentID
	a.mu.Unlock()
	// Skip if the session was replaced (new/resumed/closed) while generating.
	if current != sctx.ID || ws == "" {
		return
	}
	if err := sessions.SaveTitle(ws, sctx.ID, title); err != nil {
		return
	}
	sctx.Emit(EventSessionRenamed, SessionRenamed{ID: sctx.ID, Title: title})
}

// Interrupt cancels the in-flight turn and denies any pending approval prompts.
func (a *App) Interrupt() error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return nil // interrupting nothing is a no-op (pre-host behavior)
	}
	if err := a.mgr.Interrupt(id); err != nil && !errors.Is(err, host.ErrSessionNotFound) {
		return wireError(err)
	}
	return nil
}

// ResolvePermission delivers the user's decision for a pending approval request.
func (a *App) ResolvePermission(id, decision string) error {
	a.mu.Lock()
	sessionID := a.currentID
	a.mu.Unlock()
	if sessionID == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.ResolvePermission(sessionID, id, decision))
}

// SetPermissionMode switches the permission mode at runtime.
func (a *App) SetPermissionMode(mode string) error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.SetPermissionMode(id, mode))
}

// SaveSettings persists the settings form and applies what a running session can
// change without a rebuild (model, permission mode). Connection settings
// (provider, base URL, API key, max tokens) are stored and take effect on the
// next New/Resume session. It returns the (possibly updated) session status; an
// empty status with nil error means the settings were saved with no live session.
func (a *App) SaveSettings(req StartSessionRequest) (SessionInfo, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if strings.TrimSpace(req.CWD) == "" {
		req.CWD = ws
	}

	// Persist for next launch (carries the API key, written 0600 by saveConfig).
	saveConfig(req)

	// Rebuild the stored engine config so a subsequent New/Resume session adopts the
	// new connection settings; the workspace stays put. applyPassport mirrors
	// StartSession so a persisted provider:"passport" config keeps its Bridge
	// wiring (BaseURL/TokenSource) instead of degrading to a literal "passport"
	// provider the engine cannot build.
	if ws != "" {
		if cfg, err := buildConfig(req); err == nil {
			cfg.CWD = ws
			cfg = a.applyPassport(cfg, req)
			if strings.EqualFold(strings.TrimSpace(req.Provider), "passport") && !a.tokens.LoggedIn() {
				return SessionInfo{}, wireError(errors.New("未登录通行证，请先登录后再选择平台模型"))
			}
			a.mu.Lock()
			a.config = cfg
			a.mu.Unlock()
		}
	}

	// Apply what the live session supports immediately.
	if m := strings.TrimSpace(req.Model); m != "" {
		_ = a.SetModel(m)
	}
	if req.PermissionMode != "" {
		_ = a.SetPermissionMode(req.PermissionMode)
	}

	info, err := a.Status()
	if err != nil {
		return SessionInfo{}, nil
	}
	return info, nil
}

// SetModel switches the model used for subsequent turns.
func (a *App) SetModel(model string) error {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return wireError(errNoSession)
	}
	return wireError(a.mgr.SetModel(id, strings.TrimSpace(model)))
}

// SwitchModel changes the model for the running session, spanning both platform
// (passport) and custom direct-connection models so the in-chat picker can offer
// either. A platform model that stays on the current bridge connection is swapped
// in place (cheap, no history reload). Any switch that changes the connection —
// picking a custom model, or returning to a platform model from a custom-model
// session — rebuilds the session against the new endpoint and resumes the current
// conversation so history is preserved. Kind is "custom" for a custom model (name
// is its display name) or "platform"/"" for a passport model (name is its id).
//
// A connection-changing switch is refused mid-turn, since the rebuild would
// discard the running turn; the picker is disabled while a turn is in flight, so
// this only guards against races.
func (a *App) SwitchModel(kind, name string) (SessionInfo, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if name == "" {
		return SessionInfo{}, wireError(errors.New("模型为空"))
	}
	a.startMu.Lock()
	defer a.startMu.Unlock()
	a.mu.Lock()
	id := a.currentID
	cfg := a.config
	busy := a.turnActive
	tenant := a.passportTenant
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	pc := passportConfig()
	onBridge := cfg.Provider == "openai" && strings.HasPrefix(cfg.BaseURL, pc.BridgeBaseURL)

	if kind == "custom" {
		cm, ok := a.findCustomModel(name)
		if !ok {
			return SessionInfo{}, wireError(fmt.Errorf("自定义模型不存在: %s", name))
		}
		if busy {
			return SessionInfo{}, wireError(errBusy)
		}
		cfg.Provider = "openai"
		cfg.BaseURL = cm.BaseURL
		cfg.APIKey = cm.APIKey
		cfg.AuthToken = ""
		// Custom models are direct connections, not the passport bridge — drop the
		// token wiring so no login credential leaks to a third-party endpoint.
		cfg.TokenSource = nil
		cfg.OnUnauthorized = nil
		cfg.Model = cm.Model
		return a.rebuildResumingHeld(cfg, id)
	}

	// Platform (passport) model. Staying on the current bridge connection is just a
	// model-id swap; no rebuild, no history reload.
	if onBridge {
		if err := a.mgr.SetModel(id, name); err != nil {
			return SessionInfo{}, wireError(err)
		}
		a.mu.Lock()
		a.config.Model = name
		a.mu.Unlock()
		return a.Status()
	}
	// Coming from a custom-model session: rebuild as a passport/bridge session,
	// re-wiring the token source (mirrors applyPassport, which reads the request).
	if !a.tokens.LoggedIn() {
		return SessionInfo{}, wireError(errors.New("未登录通行证，无法切换到平台模型"))
	}
	if busy {
		return SessionInfo{}, wireError(errBusy)
	}
	cfg.Provider = "openai"
	cfg.BaseURL = pc.BridgeBaseURL + tenantPathPrefix(tenant) + "/v1"
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.TokenSource = a.tokens.Token
	cfg.OnUnauthorized = a.tokens.ForceRefresh
	cfg.Model = name
	return a.rebuildResumingHeld(cfg, id)
}

// rebuildResumingHeld rebuilds the session from cfg while resuming the current
// conversation (resumeID), so a connection swap (model/tenant/endpoint change)
// keeps history. The caller must hold startMu and have set cfg's
// provider/endpoint/model fields.
func (a *App) rebuildResumingHeld(cfg engine.Config, resumeID string) (SessionInfo, error) {
	cfg.Resume = resumeID
	cfg.Continue = false
	cfg.SessionID = ""
	info, err := a.openSessionHeld(cfg)
	if err != nil {
		return SessionInfo{}, wireError(err)
	}
	return info, nil
}

// Compact summarizes the oldest turns now and reports the message counts.
func (a *App) Compact() (CompactResult, error) {
	session, err := a.engineSession()
	if err != nil {
		return CompactResult{}, wireError(err)
	}
	before, after, usage, err := session.Compact(context.Background())
	if err != nil {
		return CompactResult{}, wireError(err)
	}
	return CompactResult{
		Before:        before,
		After:         after,
		ContextTokens: session.EstimateContextTokens(),
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
	}, nil
}

// SetPlanMode toggles plan mode on the active session and returns the updated
// status so the UI reflects it.
func (a *App) SetPlanMode(on bool) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	if err := a.mgr.SetPlanMode(id, on); err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.Status()
}

// SetReasoningScenario switches the in-conversation "thinking model"
// (off/auto/<scenario>) and returns the updated status.
func (a *App) SetReasoningScenario(scenario string) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	if err := a.mgr.SetReasoningScenario(id, scenario); err != nil {
		return SessionInfo{}, wireError(err)
	}
	return a.Status()
}

// SetThinkingEffort switches provider-native reasoning strength
// (off/low/medium/high) at runtime and returns the updated status. This is the
// knob that makes a reasoning model emit the reasoning content shown in the UI.
func (a *App) SetThinkingEffort(effort string) (SessionInfo, error) {
	a.mu.Lock()
	id := a.currentID
	a.mu.Unlock()
	if id == "" {
		return SessionInfo{}, wireError(errNoSession)
	}
	if err := a.mgr.SetThinkingEffort(id, effort); err != nil {
		return SessionInfo{}, wireError(err)
	}
	// Keep the recorded config in sync so a later NewSession/ResumeSession reuses the
	// new choice, and persist it so the choice is sticky across restarts (otherwise
	// the reasoning model reverts to emitting no thinking). The value already
	// validated inside SetThinkingEffort.
	parsed, _ := llm.ParseThinkingEffort(strings.ToLower(strings.TrimSpace(effort)))
	a.mu.Lock()
	a.config.Thinking = llm.ThinkingConfig{Effort: parsed}
	a.mu.Unlock()
	a.persistThinkingEffort(string(parsed))
	return a.Status()
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
