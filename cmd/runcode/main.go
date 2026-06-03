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

It runs with a ReAct + Tool Use main loop, streaming tool execution,
multi-provider LLM support (Anthropic, OpenAI), and MCP integration.

This is the v0.1 alpha scaffold.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(versionCmd())
	root.AddCommand(chatCmd())
	root.AddCommand(tuiCmd())
	root.AddCommand(configCmd())

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
