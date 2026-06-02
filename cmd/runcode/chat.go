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
	cmd.Flags().String("session-id", "", "Session id for transcript files")
	cmd.Flags().Int("max-history-messages", 0, "Maximum number of history messages to retain (0 = unlimited)")
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
	provider, err := stringFlagOrEnv(cmd, "provider", "RUNCODE_PROVIDER", anthropicProvider)
	if err != nil {
		return chatConfig{}, err
	}
	if provider != anthropicProvider {
		return chatConfig{}, fmt.Errorf("unsupported provider %q", provider)
	}

	model, err := stringFlagOrEnv(cmd, "model", "ANTHROPIC_MODEL", "")
	if err != nil {
		return chatConfig{}, err
	}
	if strings.TrimSpace(model) == "" {
		return chatConfig{}, errors.New("model is required")
	}

	maxTokens, err := intFlagOrEnv(cmd, "max-tokens", "ANTHROPIC_MAX_TOKENS")
	if err != nil {
		return chatConfig{}, err
	}
	baseURL, err := stringFlagOrEnv(cmd, "base-url", "ANTHROPIC_BASE_URL", "")
	if err != nil {
		return chatConfig{}, err
	}
	apiKey, authToken, err := credentialConfig(cmd)
	if err != nil {
		return chatConfig{}, err
	}
	if apiKey == "" && authToken == "" {
		return chatConfig{}, errors.New("anthropic api key or auth token is required")
	}
	cwd, err := cwdConfig(cmd)
	if err != nil {
		return chatConfig{}, err
	}
	telemetryMode, err := stringFlagOrEnv(cmd, "telemetry", "RUNCODE_TELEMETRY", "off")
	if err != nil {
		return chatConfig{}, err
	}
	telemetryMode, err = normalizeTelemetryMode(telemetryMode)
	if err != nil {
		return chatConfig{}, err
	}
	permissionMode, err := stringFlagOrEnv(cmd, "permission-mode", "RUNCODE_PERMISSION_MODE", "safe")
	if err != nil {
		return chatConfig{}, err
	}
	permissionMode, err = normalizePermissionMode(permissionMode)
	if err != nil {
		return chatConfig{}, err
	}
	transcriptMode, err := stringFlagOrEnv(cmd, "transcript", "RUNCODE_TRANSCRIPT", "off")
	if err != nil {
		return chatConfig{}, err
	}
	transcriptMode, err = normalizeTranscriptMode(transcriptMode)
	if err != nil {
		return chatConfig{}, err
	}
	sessionID, err := stringFlagOrEnv(cmd, "session-id", "RUNCODE_SESSION_ID", "")
	if err != nil {
		return chatConfig{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID != "" {
		if err := transcript.ValidateSessionID(sessionID); err != nil {
			return chatConfig{}, err
		}
	}
	maxHistoryMessages, err := intFlagOrEnv(cmd, "max-history-messages", "RUNCODE_MAX_HISTORY_MESSAGES")
	if err != nil {
		return chatConfig{}, err
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
		SessionID:          sessionID,
		MaxHistoryMessages: maxHistoryMessages,
	}, nil
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

func credentialConfig(cmd *cobra.Command) (string, string, error) {
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
	return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")), "", nil
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

func transcriptRecorder(cfg chatConfig) (transcript.Recorder, string, error) {
	if cfg.Transcript != "jsonl" {
		return transcript.Noop(), cfg.SessionID, nil
	}
	sessionID := cfg.SessionID
	if sessionID == "" {
		sessionID = transcript.NewSessionID()
	}
	recorder, err := transcript.OpenJSONL(cfg.CWD, sessionID)
	if err != nil {
		return nil, "", err
	}
	return recorder, sessionID, nil
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
	r.session = session
	return session, nil
}

func newSessionForConfig(cfg chatConfig, opts sessionFactoryOptions) (*repl.Session, sessionResources, error) {
	telemetryRuntime := opts.TelemetryRuntime
	if telemetryRuntime.Err == nil {
		telemetryRuntime = opts.Runtime
	}
	recorder := telemetryRecorderWithRuntime(cfg.Telemetry, telemetryRuntime)
	trecorder, sessionID, err := transcriptRecorder(cfg)
	resources := sessionResources{Telemetry: recorder, Transcript: trecorder, SessionID: sessionID}
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder)
		return nil, sessionResources{}, err
	}
	provider, err := anthropic.New(anthropic.Options{
		APIKey:           cfg.APIKey,
		AuthToken:        cfg.AuthToken,
		BaseURL:          cfg.BaseURL,
		DefaultMaxTokens: cfg.MaxTokens,
	})
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder)
		return nil, sessionResources{}, err
	}
	projectContext, err := loadProjectContext(cfg.CWD)
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder)
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
	})
	if err != nil {
		closeRecorders(context.Background(), recorder, trecorder)
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
	return closeRecorders(ctx, r.recorder, r.transcript)
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
			Authorizer:        permissions.InteractiveAuthorizer{Approver: newApprovalPrompter(runtime.Lines, runtime.Err)},
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
