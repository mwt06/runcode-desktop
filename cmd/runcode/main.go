// Package main is the entry point for the runcode CLI.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	version   = "0.1.0-alpha"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	root := &cobra.Command{
		Use:   "runcode",
		Short: "runcode — an open-source AI coding companion in your terminal (奔跑的代码)",
		Long: `runcode is an open-source AI coding companion CLI written in Go.

It runs as a full-screen TUI (Bubble Tea) with a ReAct + Tool Use main loop,
streaming tool execution, multi-provider LLM support (Anthropic, OpenAI),
and MCP integration.

This is the v0.1 alpha scaffold — the chat command is not yet wired.
See https://github.com/wt68/runcode for status.`,
		SilenceUsage: true,
	}

	root.AddCommand(versionCmd())
	root.AddCommand(chatCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(),
				"runcode %s\n  commit: %s\n  built:  %s\n  go:     %s/%s %s\n",
				version, commit, buildDate,
				runtime.GOOS, runtime.GOARCH, runtime.Version(),
			)
		},
	}
}

func chatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chat",
		Short: "Start an interactive TUI chat session (not yet implemented)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), banner)
			fmt.Fprintln(cmd.OutOrStdout(), "chat is not implemented yet — coming in v0.1 milestone")
			return nil
		},
	}
}

const banner = `
   _____  _    _  _   _   _____  ____   _____   ______
  |  __ \| |  | || \ | | / ____|/ __ \ |  __ \ |  ____|
  | |__) | |  | ||  \| || |    | |  | || |  | || |__
  |  _  /| |  | || . ` + "`" + ` || |    | |  | || |  | ||  __|
  | | \ \| |__| || |\  || |____| |__| || |__| || |____
  |_|  \_\\____/ |_| \_| \_____|\____/ |_____/ |______|

  奔跑的代码 — v0.1-alpha`
