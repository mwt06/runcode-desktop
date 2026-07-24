package main

// chat 是 CLI 的主命令:一个读一行、跑一个回合、打印结果的循环。配置解析在
// chat_flags.go,真正执行回合的引擎适配器在 chat_runner.go——本文件只负责命令
// 装配与循环控制,便于用 fake runner 测试。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
)

// anthropicProvider is the default provider name and the one that strictly
// requires a credential. Provider names are otherwise resolved through the
// llm registry rather than enumerated here.
const anthropicProvider = "anthropic"

var errEmptyPrompt = errors.New("prompt is required")

// chatConfig is the CLI's name for the engine's resolved configuration. The
// alias lets the existing flag-resolution code and tests keep using chatConfig
// while the engine owns the canonical type that every frontend shares.
type chatConfig = engine.Config

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
	session *engine.Session
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
	cmd.Flags().String("thinking", "", "Extended thinking effort: off, low, medium, or high")
	cmd.Flags().String("system-prompt", "", "Replace the framework identity prose with this system prompt")
	cmd.Flags().String("append-system-prompt", "", "Append extra instructions to the system prompt")
	cmd.Flags().String("transcript", "", "Transcript mode: off, jsonl, or sqlite")
	cmd.Flags().String("session-id", "", "Session id for transcript and history files")
	cmd.Flags().Int("max-history-messages", 0, "Maximum number of history messages to retain (0 = unlimited)")
	cmd.Flags().Int("max-context-tokens", 0, "Context token budget that triggers compaction (0 = disabled)")
	cmd.Flags().Int("max-retries", 0, "Provider transient-failure retries (0 = default, negative = disabled)")
	cmd.Flags().Float64("input-price", 0, "Input token price per million tokens, for the /cost estimate")
	cmd.Flags().Float64("output-price", 0, "Output token price per million tokens, for the /cost estimate")
	cmd.Flags().String("resume", "", "Resume a saved session by id and continue it")
	cmd.Flags().Bool("continue", false, "Resume the most recent saved session")
	cmd.Flags().Bool("no-session", false, "Disable saving full session history for resume")
	cmd.Flags().String("session-backend", "", "Session history backend: jsonl (default) or sqlite")
	cmd.Flags().Bool("allow-mcp-sampling", false, "Allow MCP servers to request model completions (sampling); off by default, always denied in safe mode")
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
