package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/permissions"
	"github.com/wt68/runcode/internal/persistence/sessions"
	"github.com/wt68/runcode/internal/persistence/settings"
	"github.com/wt68/runcode/internal/persistence/transcript"
	"github.com/wt68/runcode/internal/projectctx"
	"github.com/wt68/runcode/internal/prompt"
	"github.com/wt68/runcode/internal/repl"
	"github.com/wt68/runcode/internal/telemetry"
	"github.com/wt68/runcode/pkg/llm"
	"github.com/wt68/runcode/pkg/llm/providers/anthropic"
	"github.com/wt68/runcode/pkg/tool"
	"github.com/wt68/runcode/tools"
)

const anthropicProvider = "anthropic"

var errEmptyPrompt = errors.New("prompt is required")

type chatConfig struct {
	Provider           string
	Model              string
	MaxTokens          int
	BaseURL            string
	APIKey             string
	AuthToken          string
	CWD                string
	Telemetry          string
	PermissionMode     string
	Transcript         string
	SessionID          string
	MaxHistoryMessages int
	MaxContextTokens   int
	Resume             string
	Continue           bool
	PersistSession     bool
}

type chatIO struct {
	In    io.Reader
	Lines lineReader
	Err   io.Writer
	Out   io.Writer
}

type chatRunner interface {
	Run(ctx context.Context, cfg chatConfig, io chatIO, prompt string) (string, error)
}

type resettableChatRunner interface {
	Reset(context.Context) error
}

type defaultChatRunner struct {
	session    *repl.Session
	recorder   telemetry.Recorder
	transcript transcript.Recorder
	sessions   sessions.Store
}

type sessionFactoryOptions struct {
	Runtime          chatIO
	TelemetryRuntime chatIO
	StreamDelta      func(string)
	ToolEvents       chan<- tool.Event
	Permissions      *permissions.Service
}

type sessionResources struct {
	Telemetry  telemetry.Recorder
	Transcript transcript.Recorder
	Sessions   sessions.Store
	SessionID  string
}

func chatCmd() *cobra.Command {
	return newChatCmd(&defaultChatRunner{})
}

func newChatCmd(runner chatRunner) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "chat [prompt]",
		Short:        "Run one provider-backed chat turn",
		Args:         cobra.ArbitraryArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := chatConfigFromCommand(cmd)
			if err != nil {
				return err
			}
			defer closeChatRunner(cmd.Context(), runner)
			loop, err := cmd.Flags().GetBool("loop")
			if err != nil {
				return err
			}
			if loop {
				lines := newLineInput(cmd.InOrStdin())
				defer lines.Close()
				runtime := chatIO{In: cmd.InOrStdin(), Lines: lines, Err: cmd.ErrOrStderr(), Out: cmd.OutOrStdout()}
				return runChatLoop(cmd, runner, cfg, runtime, args)
			}
			promptText, err := chatPrompt(cmd, args)
			if err != nil {
				return err
			}
			runtime := chatIO{In: cmd.InOrStdin(), Lines: lineReaderForConfig(cfg, cmd.InOrStdin()), Err: cmd.ErrOrStderr(), Out: cmd.OutOrStdout()}
			if closer, ok := runtime.Lines.(interface{ Close() }); ok {
				defer closer.Close()
			}
			text, err := runner.Run(cmd.Context(), cfg, runtime, promptText)
			if err != nil {
				return err
			}
			if text != "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), text)
			} else {
				_, err = fmt.Fprintln(cmd.OutOrStdout())
			}
			return err
		},
	}
	addChatConfigFlags(cmd)
	cmd.Flags().Bool("loop", false, "Run an in-memory multi-turn chat loop")
	return cmd
}

func addChatConfigFlags(cmd *cobra.Command) {
	cmd.Flags().String("provider", anthropicProvider, "LLM provider")
	cmd.Flags().String("model", "", "Model name")
	cmd.Flags().Int("max-tokens", 0, "Maximum output tokens")
	cmd.Flags().String("base-url", "", "Anthropic-compatible base URL")
	cmd.Flags().String("api-key", "", "Anthropic API key")
	cmd.Flags().String("auth-token", "", "Anthropic bearer auth token")
	cmd.Flags().String("cwd", "", "Working directory for tools")
	cmd.Flags().String("telemetry", "", "Telemetry mode: off or jsonl")
	cmd.Flags().String("permission-mode", "", "Permission mode: safe or interactive")
	cmd.Flags().String("transcript", "", "Transcript mode: off or jsonl")
	cmd.Flags().String("session-id", "", "Session id for transcript and history files")
	cmd.Flags().Int("max-history-messages", 0, "Maximum number of history messages to retain (0 = unlimited)")
	cmd.Flags().Int("max-context-tokens", 0, "Context token budget that triggers compaction (0 = disabled)")
	cmd.Flags().String("resume", "", "Resume a saved session by id and continue it")
	cmd.Flags().Bool("continue", false, "Resume the most recent saved session")
	cmd.Flags().Bool("no-session", false, "Disable saving full session history for resume")
}

func closeChatRunner(ctx context.Context, runner chatRunner) error {
	closer, ok := runner.(interface{ Close(context.Context) error })
	if !ok {
		return nil
	}
	return closer.Close(ctx)
}

func lineReaderForConfig(cfg chatConfig, in io.Reader) lineReader {
	if cfg.PermissionMode != "interactive" {
		return nil
	}
	return newLineInput(in)
}

func runChatLoop(cmd *cobra.Command, runner chatRunner, cfg chatConfig, runtime chatIO, args []string) error {
	prompts := initialLoopPrompts(args)
	for {
		if len(prompts) == 0 {
			if _, err := fmt.Fprint(cmd.ErrOrStderr(), "> "); err != nil {
				return err
			}
			line, err := runtime.Lines.ReadLine(cmd.Context())
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			prompts = append(prompts, strings.TrimSpace(line))
		}
		promptText := prompts[0]
		prompts = prompts[1:]
		if shouldExitChatLoop(promptText) {
			return nil
		}
		if shouldClearChatLoop(promptText) {
			if err := resetChatRunner(cmd.Context(), runner); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "history cleared"); err != nil {
				return err
			}
			continue
		}
		if promptText == "" {
			continue
		}
		text, err := runner.Run(cmd.Context(), cfg, runtime, promptText)
		if err != nil {
			return err
		}
		if text != "" {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), text); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return err
			}
		}
	}
}

func initialLoopPrompts(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	return []string{strings.TrimSpace(strings.Join(args, " "))}
}

func shouldExitChatLoop(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "/exit", "/quit", "exit", "quit":
		return true
	default:
		return false
	}
}

func shouldClearChatLoop(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "/clear")
}

func resetChatRunner(ctx context.Context, runner chatRunner) error {
	resetter, ok := runner.(resettableChatRunner)
	if !ok {
		return nil
	}
	return resetter.Reset(ctx)
}

func chatConfigFromCommand(cmd *cobra.Command) (chatConfig, error) {
	cfg, _, err := resolveChatConfig(cmd)
	if err != nil {
		return chatConfig{}, err
	}
	if cfg.Provider != anthropicProvider {
		return chatConfig{}, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return chatConfig{}, errors.New("model is required")
	}
	if cfg.APIKey == "" && cfg.AuthToken == "" {
		return chatConfig{}, errors.New("anthropic api key or auth token is required")
	}
	if cfg.SessionID != "" {
		if err := transcript.ValidateSessionID(cfg.SessionID); err != nil {
			return chatConfig{}, err
		}
	}
	if cfg.Resume != "" && cfg.Continue {
		return chatConfig{}, errors.New("--resume and --continue are mutually exclusive")
	}
	if cfg.Resume != "" && cfg.SessionID != "" {
		return chatConfig{}, errors.New("--resume and --session-id are mutually exclusive")
	}
	if cfg.Resume != "" {
		if err := transcript.ValidateSessionID(cfg.Resume); err != nil {
			return chatConfig{}, err
		}
	}
	return cfg, nil
}

// resolveChatConfig resolves every configuration value using the precedence
// flag > env > config file > default. It does not enforce required fields, so it
// is reusable by `runcode config` for read-only inspection. The returned
// settings.Resolved reports which config files were loaded.
func resolveChatConfig(cmd *cobra.Command) (chatConfig, settings.Resolved, error) {
	empty := settings.Resolved{}
	cwd, err := cwdConfig(cmd)
	if err != nil {
		return chatConfig{}, empty, err
	}
	resolved, err := settings.Load(settings.LoadOptions{CWD: cwd, UserConfigDir: userConfigDir()})
	if err != nil {
		return chatConfig{}, empty, err
	}
	file := resolved.Config

	provider, err := stringFlagEnvFile(cmd, "provider", "RUNCODE_PROVIDER", file.Provider, anthropicProvider)
	if err != nil {
		return chatConfig{}, empty, err
	}
	model, err := stringFlagEnvFile(cmd, "model", "ANTHROPIC_MODEL", file.Model, "")
	if err != nil {
		return chatConfig{}, empty, err
	}
	maxTokens, err := intFlagEnvFile(cmd, "max-tokens", "ANTHROPIC_MAX_TOKENS", file.MaxTokens)
	if err != nil {
		return chatConfig{}, empty, err
	}
	baseURL, err := stringFlagEnvFile(cmd, "base-url", "ANTHROPIC_BASE_URL", file.BaseURL, "")
	if err != nil {
		return chatConfig{}, empty, err
	}
	apiKey, authToken, err := credentialConfig(cmd, file.APIKey, file.AuthToken)
	if err != nil {
		return chatConfig{}, empty, err
	}
	telemetryMode, err := stringFlagEnvFile(cmd, "telemetry", "RUNCODE_TELEMETRY", file.Telemetry, "off")
	if err != nil {
		return chatConfig{}, empty, err
	}
	telemetryMode, err = normalizeTelemetryMode(telemetryMode)
	if err != nil {
		return chatConfig{}, empty, err
	}
	permissionMode, err := stringFlagEnvFile(cmd, "permission-mode", "RUNCODE_PERMISSION_MODE", file.PermissionMode, "safe")
	if err != nil {
		return chatConfig{}, empty, err
	}
	permissionMode, err = normalizePermissionMode(permissionMode)
	if err != nil {
		return chatConfig{}, empty, err
	}
	transcriptMode, err := stringFlagEnvFile(cmd, "transcript", "RUNCODE_TRANSCRIPT", file.Transcript, "off")
	if err != nil {
		return chatConfig{}, empty, err
	}
	transcriptMode, err = normalizeTranscriptMode(transcriptMode)
	if err != nil {
		return chatConfig{}, empty, err
	}
	sessionID, err := stringFlagOrEnv(cmd, "session-id", "RUNCODE_SESSION_ID", "")
	if err != nil {
		return chatConfig{}, empty, err
	}
	maxHistoryMessages, err := intFlagEnvFile(cmd, "max-history-messages", "RUNCODE_MAX_HISTORY_MESSAGES", file.MaxHistoryMessages)
	if err != nil {
		return chatConfig{}, empty, err
	}
	maxContextTokens, err := intFlagEnvFile(cmd, "max-context-tokens", "ANTHROPIC_MAX_CONTEXT_TOKENS", file.MaxContextTokens)
	if err != nil {
		return chatConfig{}, empty, err
	}
	resumeID, err := cmd.Flags().GetString("resume")
	if err != nil {
		return chatConfig{}, empty, err
	}
	continueSession, err := cmd.Flags().GetBool("continue")
	if err != nil {
		return chatConfig{}, empty, err
	}
	noSession, err := cmd.Flags().GetBool("no-session")
	if err != nil {
		return chatConfig{}, empty, err
	}

	return chatConfig{
		Provider:           provider,
		Model:              strings.TrimSpace(model),
		MaxTokens:          maxTokens,
		BaseURL:            strings.TrimSpace(baseURL),
		APIKey:             apiKey,
		AuthToken:          authToken,
		CWD:                cwd,
		Telemetry:          telemetryMode,
		PermissionMode:     permissionMode,
		Transcript:         transcriptMode,
		SessionID:          strings.TrimSpace(sessionID),
		MaxHistoryMessages: maxHistoryMessages,
		MaxContextTokens:   maxContextTokens,
		Resume:             strings.TrimSpace(resumeID),
		Continue:           continueSession,
		PersistSession:     !noSession,
	}, resolved, nil
}

func stringFlagOrEnv(cmd *cobra.Command, name string, env string, fallback string) (string, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetString(name)
	}
	if value := os.Getenv(env); value != "" {
		return value, nil
	}
	return fallback, nil
}

func intFlagOrEnv(cmd *cobra.Command, name string, env string) (int, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetInt(name)
	}
	value := os.Getenv(env)
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", env, err)
	}
	return parsed, nil
}

// stringFlagEnvFile resolves a string with precedence flag > env > config file > default.
func stringFlagEnvFile(cmd *cobra.Command, name string, env string, fileValue string, fallback string) (string, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetString(name)
	}
	if value := os.Getenv(env); value != "" {
		return value, nil
	}
	if strings.TrimSpace(fileValue) != "" {
		return fileValue, nil
	}
	return fallback, nil
}

// intFlagEnvFile resolves an int with precedence flag > env > config file > default(0).
func intFlagEnvFile(cmd *cobra.Command, name string, env string, fileValue *int) (int, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetInt(name)
	}
	if value := os.Getenv(env); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", env, err)
		}
		return parsed, nil
	}
	if fileValue != nil {
		return *fileValue, nil
	}
	return 0, nil
}

// userConfigDir returns the per-user config root, or "" if it cannot be determined
// (in which case the user-level config layer is simply skipped).
func userConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}

// credentialConfig resolves credentials with precedence
// flag > env > user-level config file. Project-level config files never supply
// credentials (settings.Load already strips them).
func credentialConfig(cmd *cobra.Command, fileAPIKey string, fileAuthToken string) (string, string, error) {
	if cmd.Flags().Changed("auth-token") {
		value, err := cmd.Flags().GetString("auth-token")
		return "", strings.TrimSpace(value), err
	}
	if cmd.Flags().Changed("api-key") {
		value, err := cmd.Flags().GetString("api-key")
		return strings.TrimSpace(value), "", err
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); value != "" {
		return "", value, nil
	}
	if value := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); value != "" {
		return value, "", nil
	}
	if value := strings.TrimSpace(fileAuthToken); value != "" {
		return "", value, nil
	}
	if value := strings.TrimSpace(fileAPIKey); value != "" {
		return value, "", nil
	}
	return "", "", nil
}

func cwdConfig(cmd *cobra.Command) (string, error) {
	cwd, err := stringFlagOrEnv(cmd, "cwd", "RUNCODE_CWD", "")
	if err != nil {
		return "", err
	}
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(cwd)
}

func normalizeTelemetryMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "none":
		return "off", nil
	case "jsonl", "stderr-jsonl":
		return "jsonl", nil
	default:
		return "", fmt.Errorf("unsupported telemetry mode %q", value)
	}
}

func normalizePermissionMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "safe", "non-interactive":
		return "safe", nil
	case "interactive", "confirm":
		return "interactive", nil
	default:
		return "", fmt.Errorf("unsupported permission mode %q", value)
	}
}

func normalizeTranscriptMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "off", "none":
		return "off", nil
	case "jsonl":
		return "jsonl", nil
	default:
		return "", fmt.Errorf("unsupported transcript mode %q", value)
	}
}

func telemetryRecorder(mode string) telemetry.Recorder {
	return telemetryRecorderWithRuntime(mode, chatIO{Err: os.Stderr})
}

func telemetryRecorderWithRuntime(mode string, runtime chatIO) telemetry.Recorder {
	if mode == "jsonl" {
		errWriter := runtime.Err
		if errWriter == nil {
			errWriter = os.Stderr
		}
		return telemetry.NewAsync(telemetry.NewJSONL(errWriter), telemetry.AsyncOptions{BufferSize: 256})
	}
	return telemetry.Noop()
}

// resolveSessionID determines the id for this session, honoring --resume and
// --continue, falling back to --session-id or a freshly generated id.
func resolveSessionID(cfg chatConfig) (string, error) {
	if cfg.Resume != "" {
		return cfg.Resume, nil
	}
	if cfg.Continue {
		latest, err := sessions.LatestSessionID(cfg.CWD)
		if err != nil {
			return "", err
		}
		if latest == "" {
			return "", errors.New("no saved session to continue")
		}
		return latest, nil
	}
	if cfg.SessionID != "" {
		return cfg.SessionID, nil
	}
	return transcript.NewSessionID(), nil
}

func transcriptRecorderForID(cfg chatConfig, sessionID string) (transcript.Recorder, error) {
	if cfg.Transcript != "jsonl" {
		return transcript.Noop(), nil
	}
	return transcript.OpenJSONL(cfg.CWD, sessionID)
}

func openSessionStore(cfg chatConfig, sessionID string) (sessions.Store, error) {
	if !cfg.PersistSession {
		return sessions.Noop(), nil
	}
	return sessions.OpenJSONL(cfg.CWD, sessionID)
}

func chatPrompt(cmd *cobra.Command, args []string) (string, error) {
	if len(args) > 0 {
		text := strings.TrimSpace(strings.Join(args, " "))
		if text == "" {
			return "", errEmptyPrompt
		}
		return text, nil
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", errEmptyPrompt
	}
	return text, nil
}

func (r *defaultChatRunner) Run(ctx context.Context, cfg chatConfig, runtime chatIO, userPrompt string) (string, error) {
	session, err := r.sessionFor(cfg, runtime)
	if err != nil {
		return "", err
	}
	result, err := session.RunTurn(ctx, userPrompt)
	if err != nil {
		return "", err
	}
	if runtime.Out != nil {
		return "", nil
	}
	return llm.TextContent(result.FinalAssistant), nil
}

func (r *defaultChatRunner) sessionFor(cfg chatConfig, runtime chatIO) (*repl.Session, error) {
	if r.session != nil {
		return r.session, nil
	}
	session, resources, err := newSessionForConfig(cfg, sessionFactoryOptions{Runtime: runtime})
	if err != nil {
		return nil, err
	}
	r.recorder = resources.Telemetry
	r.transcript = resources.Transcript
	r.sessions = resources.Sessions
	r.session = session
	return session, nil
}

func newSessionForConfig(cfg chatConfig, opts sessionFactoryOptions) (*repl.Session, sessionResources, error) {
	telemetryRuntime := opts.TelemetryRuntime
	if telemetryRuntime.Err == nil {
		telemetryRuntime = opts.Runtime
	}
	recorder := telemetryRecorderWithRuntime(cfg.Telemetry, telemetryRuntime)
	sessionID, err := resolveSessionID(cfg)
	if err != nil {
		closeRecorders(context.Background(), recorder)
		return nil, sessionResources{}, err
	}
	trecorder, err := transcriptRecorderForID(cfg, sessionID)
	if err != nil {
		closeRecorders(context.Background(), recorder)
		return nil, sessionResources{}, err
	}
	store, err := openSessionStore(cfg, sessionID)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder)
		return nil, sessionResources{}, err
	}
	var initialHistory []llm.Message
	if cfg.Resume != "" || cfg.Continue {
		initialHistory, err = sessions.LoadHistory(cfg.CWD, sessionID)
		if err != nil {
			closeRecorders(context.Background(), recorder, trecorder, store)
			return nil, sessionResources{}, err
		}
	}
	resources := sessionResources{Telemetry: recorder, Transcript: trecorder, Sessions: store, SessionID: sessionID}
	provider, err := anthropic.New(anthropic.Options{
		APIKey:           cfg.APIKey,
		AuthToken:        cfg.AuthToken,
		BaseURL:          cfg.BaseURL,
		DefaultMaxTokens: cfg.MaxTokens,
	})
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store)
		return nil, sessionResources{}, err
	}
	projectContext, err := loadProjectContext(cfg.CWD)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store)
		return nil, sessionResources{}, err
	}
	permissionService := opts.Permissions
	if permissionService == nil {
		permissionService = permissionServiceForMode(cfg.PermissionMode, opts.Runtime)
	}
	streamDelta := opts.StreamDelta
	if streamDelta == nil && opts.Runtime.Out != nil {
		out := opts.Runtime.Out
		streamDelta = func(delta string) { fmt.Fprint(out, delta) }
	}
	session, err := repl.NewSession(repl.SessionOptions{
		Provider:  provider,
		Model:     cfg.Model,
		Tools:     tools.Builtins(),
		MaxTokens: cfg.MaxTokens,
		Prompt: prompt.AssemblerOpts{
			CWD:            cfg.CWD,
			Date:           time.Now().Format("2006-01-02"),
			ShellInfo:      shellInfo(),
			ProjectCtx:     projectContext,
			PermissionMode: cfg.PermissionMode,
		},
		ToolContext: &tool.Context{
			WorkingDirectory: cfg.CWD,
			ReadSet:          map[string]tool.ReadFile{},
		},
		ToolEvents:         opts.ToolEvents,
		Telemetry:          recorder,
		Permissions:        permissionService,
		Transcript:         trecorder,
		SessionID:          sessionID,
		MaxHistoryMessages: cfg.MaxHistoryMessages,
		StreamDelta:        streamDelta,
		InitialHistory:     initialHistory,
		SessionStore:       store,
		MaxContextTokens:   cfg.MaxContextTokens,
	})
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder, store)
		return nil, sessionResources{}, err
	}
	return session, resources, nil
}

func (r *defaultChatRunner) Reset(context.Context) error {
	if r.session == nil {
		return nil
	}
	r.session.ResetHistory()
	return nil
}

func (r *defaultChatRunner) Close(ctx context.Context) error {
	return closeRecorders(ctx, r.recorder, r.transcript, r.sessions)
}

func closeRecorders(ctx context.Context, recorders ...interface{ Close(context.Context) error }) error {
	var closeErr error
	for _, recorder := range recorders {
		if recorder == nil {
			continue
		}
		if err := recorder.Close(ctx); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func loadProjectContext(cwd string) (string, error) {
	result, err := projectctx.Load(projectctx.LoadOptions{CWD: cwd})
	if err != nil {
		return "", fmt.Errorf("load project context: %w", err)
	}
	return projectctx.Format(result), nil
}

func permissionServiceForMode(mode string, runtime chatIO) *permissions.Service {
	if mode == "interactive" {
		return permissions.NewService(permissions.Options{
			Mode:              mode,
			ApprovalAvailable: true,
			Authorizer: permissions.InteractiveAuthorizer{
				Approver: newApprovalPrompter(runtime.Lines, runtime.Err),
				Store:    permissions.NewMemorySessionAllowStore(),
			},
		})
	}
	return permissions.NewService(permissions.Options{Mode: mode})
}

func shellInfo() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	if shell := os.Getenv("COMSPEC"); shell != "" {
		return shell
	}
	return "bash"
}
