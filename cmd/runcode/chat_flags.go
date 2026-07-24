package main

// chat 命令的配置解析:把 cobra 标志、环境变量与配置文件按"标志 > 环境变量 >
// 配置文件 > 默认值"的优先级折叠成一份 engine.Config。全部是围绕 *cobra.Command
// 的取值与校验,与会话执行无关,所以从 chat.go 分出来单独放。

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/cost"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
	"gitlab.ouc-online.com.cn/aibase/agentloop/sessions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
	"gitlab.ouc-online.com.cn/aibase/agentloop/transcript"
)

func chatConfigFromCommand(cmd *cobra.Command) (chatConfig, error) {
	cfg, _, err := resolveChatConfig(cmd)
	if err != nil {
		return chatConfig{}, err
	}
	if !llm.IsRegistered(cfg.Provider) {
		return chatConfig{}, fmt.Errorf("unsupported provider %q (registered: %v)", cfg.Provider, llm.Registered())
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return chatConfig{}, errors.New("model is required")
	}
	// OpenAI-compatible endpoints (local vLLM/Ollama/llama.cpp, gateways) often
	// run without auth, so only Anthropic strictly requires a credential.
	if cfg.Provider == anthropicProvider && cfg.APIKey == "" && cfg.AuthToken == "" {
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
	sessionID, err := stringFlagOrEnv(cmd, "session-id", "RUNCODE_SESSION_ID")
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
	thinkingStr, err := stringFlagEnvFile(cmd, "thinking", "RUNCODE_THINKING", file.Thinking, "off")
	if err != nil {
		return chatConfig{}, empty, err
	}
	thinkingEffort, ok := llm.ParseThinkingEffort(strings.TrimSpace(thinkingStr))
	if !ok {
		return chatConfig{}, empty, fmt.Errorf("unsupported thinking effort %q (want off, low, medium, or high)", thinkingStr)
	}
	systemPrompt, err := stringFlagOrEnv(cmd, "system-prompt", "RUNCODE_SYSTEM_PROMPT")
	if err != nil {
		return chatConfig{}, empty, err
	}
	appendSystemPrompt, err := stringFlagOrEnv(cmd, "append-system-prompt", "RUNCODE_APPEND_SYSTEM_PROMPT")
	if err != nil {
		return chatConfig{}, empty, err
	}
	maxRetries, err := intFlagEnvFile(cmd, "max-retries", "RUNCODE_MAX_RETRIES", file.MaxRetries)
	if err != nil {
		return chatConfig{}, empty, err
	}
	inputPrice, err := floatFlagEnvFile(cmd, "input-price", "RUNCODE_INPUT_PRICE", file.InputPrice)
	if err != nil {
		return chatConfig{}, empty, err
	}
	outputPrice, err := floatFlagEnvFile(cmd, "output-price", "RUNCODE_OUTPUT_PRICE", file.OutputPrice)
	if err != nil {
		return chatConfig{}, empty, err
	}
	// Resolve the effective pricing: an explicit price (flag/env/config) wins; if
	// neither price is set, fall back to the built-in table by model name; an
	// unknown model stays unpriced.
	priceSource := ""
	priceExplicit := floatSet(cmd, "input-price", "RUNCODE_INPUT_PRICE", file.InputPrice) ||
		floatSet(cmd, "output-price", "RUNCODE_OUTPUT_PRICE", file.OutputPrice)
	switch {
	case priceExplicit:
		priceSource = "explicit"
	default:
		if price, ok := cost.Lookup(model); ok {
			inputPrice = price.InputPerMTok
			outputPrice = price.OutputPerMTok
			priceSource = "builtin"
		}
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
	sessionBackend, err := stringFlagEnvFile(cmd, "session-backend", "RUNCODE_SESSION_BACKEND", file.SessionBackend, sessions.BackendJSONL)
	if err != nil {
		return chatConfig{}, empty, err
	}
	sessionBackend, err = normalizeSessionBackend(sessionBackend)
	if err != nil {
		return chatConfig{}, empty, err
	}
	mcpServers, err := engine.MCPServersFromConfig(file.MCP)
	if err != nil {
		return chatConfig{}, empty, err
	}
	allowMCPSampling, err := boolFlagEnvFile(cmd, "allow-mcp-sampling", "RUNCODE_ALLOW_MCP_SAMPLING", file.MCP.AllowSampling)
	if err != nil {
		return chatConfig{}, empty, err
	}
	hookList, err := hooksFromConfig(file.Hooks)
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
		SessionBackend:     sessionBackend,
		MaxRetries:         maxRetries,
		InputPrice:         inputPrice,
		OutputPrice:        outputPrice,
		PriceSource:        priceSource,
		MCPServers:         mcpServers,
		AllowMCPSampling:   allowMCPSampling,
		Hooks:              hookList,
		Thinking:           llm.ThinkingConfig{Effort: thinkingEffort},
		SystemPrompt:       systemPrompt,
		SystemPromptAppend: appendSystemPrompt,
	}, resolved, nil
}

// stringFlagOrEnv resolves a string with precedence flag > env, defaulting to
// empty. Values with a config-file layer go through stringFlagEnvFile instead.
func stringFlagOrEnv(cmd *cobra.Command, name string, env string) (string, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetString(name)
	}
	if value := os.Getenv(env); value != "" {
		return value, nil
	}
	return "", nil
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

// boolFlagEnvFile resolves a bool with precedence flag > env > config file > default(false).
func boolFlagEnvFile(cmd *cobra.Command, name string, env string, fileValue *bool) (bool, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetBool(name)
	}
	if value := os.Getenv(env); value != "" {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "on", "yes":
			return true, nil
		case "0", "false", "off", "no":
			return false, nil
		default:
			return false, fmt.Errorf("parse %s: invalid boolean %q", env, value)
		}
	}
	if fileValue != nil {
		return *fileValue, nil
	}
	return false, nil
}

// floatFlagEnvFile resolves a float64 with precedence flag > env > config file > default(0).
// floatSet reports whether a float value was provided by any layer (flag, env,
// or config file), distinguishing an explicit 0 from an unset value — which the
// resolved float alone cannot.
func floatSet(cmd *cobra.Command, name string, env string, fileValue *float64) bool {
	return cmd.Flags().Changed(name) || os.Getenv(env) != "" || fileValue != nil
}

func floatFlagEnvFile(cmd *cobra.Command, name string, env string, fileValue *float64) (float64, error) {
	if cmd.Flags().Changed(name) {
		return cmd.Flags().GetFloat64(name)
	}
	if value := os.Getenv(env); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
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
	cwd, err := stringFlagOrEnv(cmd, "cwd", "RUNCODE_CWD")
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
	case "sqlite":
		return "sqlite", nil
	default:
		return "", fmt.Errorf("unsupported transcript mode %q (want off, jsonl, or sqlite)", value)
	}
}

func normalizeSessionBackend(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", sessions.BackendJSONL:
		return sessions.BackendJSONL, nil
	case sessions.BackendSQLite:
		return sessions.BackendSQLite, nil
	default:
		return "", fmt.Errorf("unsupported session backend %q (want %q or %q)", value, sessions.BackendJSONL, sessions.BackendSQLite)
	}
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
