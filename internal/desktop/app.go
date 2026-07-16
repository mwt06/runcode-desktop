package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/engine"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools/preview"
)

// toolEventBuffer bounds the per-session tool-event channel so a burst of tool
// activity never blocks the executor before the pump forwards events.
const toolEventBuffer = 256

var (
	errNoSession = errors.New("no active session")
	errBusy      = errors.New("a turn is already in progress")
)

// App is the desktop session manager. It owns at most one engine.Session at a
// time (single-workspace for P1), runs turns asynchronously, and forwards every
// streaming callback and tool event to the EventSink. All state transitions are
// guarded by mu; long-running work (a turn) runs on its own goroutine and reports
// back through events, so command methods never block the UI thread.
// Dialoger opens native file/folder pickers. The Wails shell implements it; the
// transport-agnostic core only needs the interface, so it stays testable.
type Dialoger interface {
	// PickFile opens a file-open dialog and returns the chosen path ("" if the user
	// cancelled).
	PickFile(title string) (string, error)
	// PickFolder opens a directory-open dialog and returns the chosen path ("" if the
	// user cancelled).
	PickFolder(title string) (string, error)
	// PickImage opens an image-file dialog and returns the chosen path ("" if the
	// user cancelled).
	PickImage(title string) (string, error)
}

type App struct {
	sink   EventSink
	dialog Dialoger

	mu         sync.Mutex
	session    *engine.Session
	approver   *Approver
	toolEvents chan tool.Event
	pumpCancel context.CancelFunc
	turnCancel context.CancelFunc
	inFlight   bool
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
	// edits captures Write/Edit pre/post content for the "已编辑" cards' undo/review.
	// One instance for the App's lifetime, rebound to each session via BeginSession.
	edits *editStore
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

// New returns an App that emits events to sink.
func New(sink EventSink) *App {
	a := &App{sink: sink, edits: newEditStore()}
	pc := passportConfig()
	a.tokens = newTokenManager(pc.tokenURL(), pc.ClientID, passportHTTP(), func() {
		a.mu.Lock()
		a.passportUser = nil
		a.mu.Unlock()
		sink.Emit(EventPassportChanged, PassportStatus{})
	})
	a.tokens.loadPersisted()
	// Publish the saved web-tool proxy before any session builds its tools, so the
	// setting is live from launch rather than only after it is re-saved.
	applyWebProxy(loadRawConfig().WebProxy)
	return a
}

// SetDialoger installs the native file-dialog provider (called by the shell).
func (a *App) SetDialoger(d Dialoger) { a.dialog = d }

// StartSession opens (or reopens) the session for a workspace and returns its
// display state. Any existing session is closed first.
func (a *App) StartSession(req StartSessionRequest) (SessionInfo, error) {
	cfg, err := buildConfig(req)
	if err != nil {
		return SessionInfo{}, err
	}
	cfg = a.applyPassport(cfg, req)
	if strings.EqualFold(strings.TrimSpace(req.Provider), "passport") && !a.tokens.LoggedIn() {
		return SessionInfo{}, errors.New("未登录通行证，请先登录后再选择平台模型")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workspace = cfg.CWD
	info, err := a.buildAndSetLocked(cfg)
	if err != nil {
		return SessionInfo{}, err
	}
	// Persist the form values so the next launch prefills them.
	saveConfig(req)
	return info, nil
}

// buildAndSetLocked closes the current session and builds a new one from cfg,
// wiring its approver, permission service, stream/tool sinks, and tool-event
// pump. The caller must hold a.mu. cfg is recorded so ResumeSession/NewSession
// can reuse the provider/model/credentials.
func (a *App) buildAndSetLocked(cfg engine.Config) (SessionInfo, error) {
	a.closeLocked(context.Background())

	approver := NewApprover(a.sink, cfg.CWD)
	store, err := engine.NewAllowStore(cfg.CWD)
	if err != nil {
		return SessionInfo{}, err
	}
	// Always mode-aware (interactive authorizer installed even in safe mode) so the
	// UI can switch modes at runtime.
	permSvc := permissions.NewService(permissions.Options{
		Mode:              cfg.PermissionMode,
		ApprovalAvailable: true,
		InteractiveAuthorizer: permissions.InteractiveAuthorizer{
			Approver: approver,
			Store:    store,
			// Model harm gate: auto-allow actions the model judges safe; only prompt
			// for ones it flags as potentially harmful (or when the check fails).
			HarmJudge: modelHarmJudge{app: a},
			// Session-scoped audit + circuit breaker: surface every smart-mode
			// auto-allow to the UI, and stop auto-allowing once the session budget is
			// spent so a fooled judge can't silently wave through an unbounded stream.
			Breaker: permissions.NewHarmBreaker(0),
			Audit:   a.emitHarmAutoAllow,
		},
	})

	toolEvents := make(chan tool.Event, toolEventBuffer)
	session, err := engine.Build(cfg, engine.Options{
		Permissions:    permSvc,
		StreamDelta:    func(delta string) { a.sink.Emit(EventAssistantDelta, AssistantDelta{Text: delta}) },
		StreamThinking: func(delta string) { a.sink.Emit(EventAssistantThinking, AssistantDelta{Text: delta}) },
		ToolEvents:     toolEvents,
		Warn:           warnWriter{sink: a.sink},
		ExtraTools:     []tool.Tool{preview.New()},
		EditRecorder:   a.edits,
	})
	if err != nil {
		return SessionInfo{}, err
	}

	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	go a.pumpToolEvents(pumpCtx, toolEvents)

	a.session = session
	a.approver = approver
	a.toolEvents = toolEvents
	a.pumpCancel = pumpCancel
	a.config = cfg
	a.edits.BeginSession(cfg.CWD, session.SessionID())

	// Restart the workspace preview server for this session (non-fatal on error).
	// closeLocked above already stopped and cleared any previous preview server.
	if ps := newPreviewServer(); cfg.CWD != "" {
		if url, err := ps.start(cfg.CWD); err == nil {
			a.preview = ps
			a.previewURL = url
		}
	}
	return a.statusLocked(), nil
}

// SendMessage runs one user turn asynchronously. It returns immediately; the
// turn's result arrives as an EventTurnEnd or EventTurnError. It errors only when
// there is no session or a turn is already running.
func (a *App) SendMessage(text string) error {
	a.mu.Lock()
	if a.session == nil {
		a.mu.Unlock()
		return errNoSession
	}
	if a.inFlight {
		a.mu.Unlock()
		return errBusy
	}
	session := a.session
	turnCtx, cancel := context.WithCancel(context.Background())
	a.turnCancel = cancel
	a.inFlight = true
	a.edits.BeginTurn()
	a.mu.Unlock()

	go func() {
		started := time.Now()
		result, err := session.RunTurn(turnCtx, text)
		a.mu.Lock()
		a.inFlight = false
		if a.turnCancel != nil {
			a.turnCancel = nil
		}
		a.mu.Unlock()
		cancel()
		if err != nil {
			a.sink.Emit(EventTurnError, TurnError{Error: err.Error()})
			return
		}
		a.sink.Emit(EventTurnEnd, turnEndFromResult(result, int(time.Since(started).Milliseconds())))
		// Name the session from this turn's question, regenerating each turn so the
		// title tracks the latest request. Runs off the turn path (its own context)
		// so it never delays the reply; failures are silent.
		a.refreshTitle(session, text)
	}()
	return nil
}

// titleGenTimeout bounds the off-turn title-generation request so a slow or stuck
// model call cannot leak a goroutine.
const titleGenTimeout = 30 * time.Second

// refreshTitle generates a short title for the session from userText, persists it
// to the session's sidecar, and announces it so the sidebar can update. It is
// best-effort: any error (model failure, closed session, write error) is ignored.
func (a *App) refreshTitle(session *engine.Session, userText string) {
	ctx, cancel := context.WithTimeout(context.Background(), titleGenTimeout)
	defer cancel()
	title, err := session.GenerateTitle(ctx, userText)
	if err != nil || strings.TrimSpace(title) == "" {
		return
	}
	a.mu.Lock()
	ws := a.workspace
	current := a.session
	a.mu.Unlock()
	// Skip if the session was replaced (new/resumed/closed) while generating.
	if current != session || ws == "" {
		return
	}
	id := session.SessionID()
	if err := sessions.SaveTitle(ws, id, title); err != nil {
		return
	}
	a.sink.Emit(EventSessionRenamed, SessionRenamed{ID: id, Title: title})
}

// Interrupt cancels the in-flight turn and denies any pending approval prompts.
func (a *App) Interrupt() error {
	a.mu.Lock()
	cancel := a.turnCancel
	approver := a.approver
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if approver != nil {
		approver.DenyAll()
	}
	return nil
}

// ResolvePermission delivers the user's decision for a pending approval request.
func (a *App) ResolvePermission(id, decision string) error {
	a.mu.Lock()
	approver := a.approver
	a.mu.Unlock()
	if approver == nil {
		return errNoSession
	}
	return approver.Resolve(id, decision)
}

// SetPermissionMode switches the permission mode at runtime.
func (a *App) SetPermissionMode(mode string) error {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session == nil {
		return errNoSession
	}
	return session.SetPermissionMode(mode)
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
				return SessionInfo{}, errors.New("未登录通行证，请先登录后再选择平台模型")
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
	session := a.session
	a.mu.Unlock()
	if session == nil {
		return errNoSession
	}
	return session.SetModel(strings.TrimSpace(model))
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
		return SessionInfo{}, errors.New("模型为空")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return SessionInfo{}, errNoSession
	}
	pc := passportConfig()
	onBridge := a.config.Provider == "openai" && strings.HasPrefix(a.config.BaseURL, pc.BridgeBaseURL)

	if kind == "custom" {
		cm, ok := a.findCustomModel(name)
		if !ok {
			return SessionInfo{}, fmt.Errorf("自定义模型不存在: %s", name)
		}
		if a.inFlight {
			return SessionInfo{}, errBusy
		}
		cfg := a.config
		cfg.Provider = "openai"
		cfg.BaseURL = cm.BaseURL
		cfg.APIKey = cm.APIKey
		cfg.AuthToken = ""
		// Custom models are direct connections, not the passport bridge — drop the
		// token wiring so no login credential leaks to a third-party endpoint.
		cfg.TokenSource = nil
		cfg.OnUnauthorized = nil
		cfg.Model = cm.Model
		return a.rebuildResumingLocked(cfg)
	}

	// Platform (passport) model. Staying on the current bridge connection is just a
	// model-id swap; no rebuild, no history reload.
	if onBridge {
		if err := a.session.SetModel(name); err != nil {
			return SessionInfo{}, err
		}
		a.config.Model = name
		return a.statusLocked(), nil
	}
	// Coming from a custom-model session: rebuild as a passport/bridge session,
	// re-wiring the token source (inlined from applyPassport, which re-locks a.mu).
	if !a.tokens.LoggedIn() {
		return SessionInfo{}, errors.New("未登录通行证，无法切换到平台模型")
	}
	if a.inFlight {
		return SessionInfo{}, errBusy
	}
	cfg := a.config
	cfg.Provider = "openai"
	cfg.BaseURL = pc.BridgeBaseURL + tenantPathPrefix(a.passportTenant) + "/v1"
	cfg.APIKey = ""
	cfg.AuthToken = ""
	cfg.TokenSource = a.tokens.Token
	cfg.OnUnauthorized = a.tokens.ForceRefresh
	cfg.Model = name
	return a.rebuildResumingLocked(cfg)
}

// rebuildResumingLocked rebuilds the session from cfg while resuming the current
// conversation, so a connection swap (model/tenant/endpoint change) keeps history.
// The caller must hold a.mu and have set cfg's provider/endpoint/model fields.
func (a *App) rebuildResumingLocked(cfg engine.Config) (SessionInfo, error) {
	cfg.Resume = a.session.SessionID()
	cfg.Continue = false
	cfg.SessionID = ""
	return a.buildAndSetLocked(cfg)
}

// Compact summarizes the oldest turns now and reports the message counts.
func (a *App) Compact() (CompactResult, error) {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session == nil {
		return CompactResult{}, errNoSession
	}
	before, after, err := session.Compact(context.Background())
	if err != nil {
		return CompactResult{}, err
	}
	return CompactResult{Before: before, After: after}, nil
}

// SetPlanMode toggles plan mode on the active session and returns the updated
// status so the UI reflects it.
func (a *App) SetPlanMode(on bool) (SessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return SessionInfo{}, errNoSession
	}
	a.session.SetPlanMode(on)
	return a.statusLocked(), nil
}

// SetReasoningScenario switches the in-conversation "thinking model"
// (off/auto/<scenario>) and returns the updated status.
func (a *App) SetReasoningScenario(scenario string) (SessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return SessionInfo{}, errNoSession
	}
	a.session.SetReasoningScenario(scenario)
	return a.statusLocked(), nil
}

// SetThinkingEffort switches provider-native reasoning strength
// (off/low/medium/high) at runtime and returns the updated status. This is the
// knob that makes a reasoning model emit the reasoning content shown in the UI.
func (a *App) SetThinkingEffort(effort string) (SessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return SessionInfo{}, errNoSession
	}
	if err := a.session.SetThinkingEffort(effort); err != nil {
		return SessionInfo{}, err
	}
	// Keep the recorded config in sync so a later NewSession/ResumeSession reuses the
	// new choice, and persist it so the choice is sticky across restarts (otherwise
	// the reasoning model reverts to emitting no thinking). The value already
	// validated inside SetThinkingEffort.
	parsed, _ := llm.ParseThinkingEffort(strings.ToLower(strings.TrimSpace(effort)))
	a.config.Thinking = llm.ThinkingConfig{Effort: parsed}
	a.persistThinkingEffort(string(parsed))
	return a.statusLocked(), nil
}

// Reset clears the in-memory working history (the on-disk log is untouched).
func (a *App) Reset() error {
	a.mu.Lock()
	session := a.session
	a.mu.Unlock()
	if session == nil {
		return errNoSession
	}
	session.ResetHistory()
	return nil
}

// Status returns the current session's display state.
func (a *App) Status() (SessionInfo, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return SessionInfo{}, errNoSession
	}
	return a.statusLocked(), nil
}

// CloseSession ends the session and releases its resources.
func (a *App) CloseSession() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeLocked(context.Background())
	return nil
}

func (a *App) statusLocked() SessionInfo {
	st := a.session.Status()
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
		PreviewBaseURL:     a.previewURL,
	}
}

// closeLocked tears down the current session. The caller must hold a.mu. It
// cancels the turn, denies pending approvals, stops the tool-event pump, and
// closes the engine session; it is safe to call when there is no session.
func (a *App) closeLocked(ctx context.Context) {
	if a.turnCancel != nil {
		a.turnCancel()
		a.turnCancel = nil
	}
	if a.approver != nil {
		a.approver.DenyAll()
		a.approver = nil
	}
	if a.pumpCancel != nil {
		a.pumpCancel()
		a.pumpCancel = nil
	}
	if a.session != nil {
		_ = a.session.Close(ctx)
		a.session = nil
	}
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
	a.toolEvents = nil
	a.inFlight = false
}

func (a *App) pumpToolEvents(ctx context.Context, ch <-chan tool.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			a.sink.Emit(EventToolEvent, ev)
		}
	}
}

// warnWriter is an io.Writer that emits each write as a Warning event, so the
// engine's startup diagnostics reach the UI.
type warnWriter struct {
	sink EventSink
}

func (w warnWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimRight(string(p), "\n"); msg != "" {
		w.sink.Emit(EventWarning, Warning{Message: msg})
	}
	return len(p), nil
}
