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
	Provider       string
	Model          string
	MaxTokens      int
	BaseURL        string
	APIKey         string
	AuthToken      string
	CWD            string
	Telemetry      string
	PermissionMode string
}

type chatIO struct {
	In  io.Reader
	Err io.Writer
}

type chatRunner interface {
	Run(ctx context.Context, cfg chatConfig, io chatIO, prompt string) (string, error)
}

type defaultChatRunner struct{}

func chatCmd() *cobra.Command {
	return newChatCmd(defaultChatRunner{})
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
			promptText, err := chatPrompt(cmd, args)
			if err != nil {
				return err
			}
			text, err := runner.Run(cmd.Context(), cfg, chatIO{In: cmd.InOrStdin(), Err: cmd.ErrOrStderr()}, promptText)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), text)
			return err
		},
	}
	cmd.Flags().String("provider", anthropicProvider, "LLM provider")
	cmd.Flags().String("model", "", "Model name")
	cmd.Flags().Int("max-tokens", 0, "Maximum output tokens")
	cmd.Flags().String("base-url", "", "Anthropic-compatible base URL")
	cmd.Flags().String("api-key", "", "Anthropic API key")
	cmd.Flags().String("auth-token", "", "Anthropic bearer auth token")
	cmd.Flags().String("cwd", "", "Working directory for tools")
	cmd.Flags().String("telemetry", "", "Telemetry mode: off or jsonl")
	cmd.Flags().String("permission-mode", "", "Permission mode: safe or interactive")
	return cmd
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

	return chatConfig{
		Provider:       provider,
		Model:          strings.TrimSpace(model),
		MaxTokens:      maxTokens,
		BaseURL:        strings.TrimSpace(baseURL),
		APIKey:         apiKey,
		AuthToken:      authToken,
		CWD:            cwd,
		Telemetry:      telemetryMode,
		PermissionMode: permissionMode,
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

func telemetryRecorder(mode string) telemetry.Recorder {
	if mode == "jsonl" {
		return telemetry.NewAsync(telemetry.NewJSONL(os.Stderr), telemetry.AsyncOptions{BufferSize: 256})
	}
	return telemetry.Noop()
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

func (defaultChatRunner) Run(ctx context.Context, cfg chatConfig, runtime chatIO, userPrompt string) (string, error) {
	recorder := telemetryRecorder(cfg.Telemetry)
	defer recorder.Close(context.Background())
	provider, err := anthropic.New(anthropic.Options{
		APIKey:           cfg.APIKey,
		AuthToken:        cfg.AuthToken,
		BaseURL:          cfg.BaseURL,
		DefaultMaxTokens: cfg.MaxTokens,
	})
	if err != nil {
		return "", err
	}
	builtins := tools.Builtins()
	permissionService := permissionServiceForMode(cfg.PermissionMode, runtime)
	session, err := repl.NewSession(repl.SessionOptions{
		Provider:  provider,
		Model:     cfg.Model,
		Tools:     builtins,
		MaxTokens: cfg.MaxTokens,
		Prompt: prompt.AssemblerOpts{
			CWD:       cfg.CWD,
			Date:      time.Now().Format("2006-01-02"),
			ShellInfo: shellInfo(),
		},
		ToolContext: &tool.Context{
			WorkingDirectory: cfg.CWD,
			ReadSet:          map[string]tool.ReadFile{},
		},
		Telemetry:   recorder,
		Permissions: permissionService,
	})
	if err != nil {
		return "", err
	}
	result, err := session.RunTurn(ctx, userPrompt)
	if err != nil {
		return "", err
	}
	return llm.TextContent(result.FinalAssistant), nil
}

func permissionServiceForMode(mode string, runtime chatIO) *permissions.Service {
	if mode == "interactive" {
		return permissions.NewService(permissions.Options{
			Mode:              mode,
			ApprovalAvailable: true,
			Authorizer:        permissions.InteractiveAuthorizer{Approver: newApprovalPrompter(runtime.In, runtime.Err)},
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
