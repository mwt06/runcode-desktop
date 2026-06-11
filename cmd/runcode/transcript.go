package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wt68/runcode/internal/persistence/transcript"
)

// transcript previews are bounded so a search/list stays one tidy line per turn.
const transcriptPreviewRunes = 100

// transcriptCmd browses and searches the sanitized SQLite transcript at
// <workspace>/.runcode/transcripts.db, written when running with
// `--transcript sqlite`. The transcript is an audit log (whitelisted fields), not
// the loss-less session history that `runcode sessions` reads.
func transcriptCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "transcript",
		Short:        "Search and list recorded transcripts (.runcode/transcripts.db)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTranscriptList(cmd)
		},
	}
	cmd.PersistentFlags().String("cwd", "", "workspace directory (default: current directory; env RUNCODE_CWD)")
	cmd.AddCommand(transcriptListCmd())
	cmd.AddCommand(transcriptSearchCmd())
	return cmd
}

func transcriptListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List sessions recorded in the transcript database",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTranscriptList(cmd)
		},
	}
}

func transcriptSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "search <query>",
		Short:        "Search recorded turns by user/assistant text (newest first)",
		Args:         cobra.MinimumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTranscriptSearch(cmd, strings.Join(args, " "))
		},
	}
	cmd.Flags().String("session", "", "restrict the search to one session id")
	cmd.Flags().Int("limit", 0, "maximum results (default 50)")
	cmd.Flags().Bool("tool", false, "match only tool names/commands (e.g. a Bash command), not prose")
	return cmd
}

// openTranscriptRecorder resolves the workspace and opens its transcript database,
// but only if one exists — a read command should not create an empty database or
// imply transcripts were recorded when they were not. ok=false means there is no
// database (the caller prints a hint).
func openTranscriptRecorder(cmd *cobra.Command) (rec *transcript.SQLiteRecorder, ok bool, err error) {
	cwd, err := cwdConfig(cmd)
	if err != nil {
		return nil, false, err
	}
	exists, err := transcript.HasSQLite(cwd)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	rec, err = transcript.OpenSQLite(cwd)
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

func transcriptHint(w io.Writer) {
	fmt.Fprintln(w, "No SQLite transcript in this workspace.")
	fmt.Fprintln(w, "Record one by running with `--transcript sqlite` (or set RUNCODE_TRANSCRIPT=sqlite / transcript=\"sqlite\" in config).")
}

func runTranscriptList(cmd *cobra.Command) error {
	rec, ok, err := openTranscriptRecorder(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !ok {
		transcriptHint(out)
		return nil
	}
	defer rec.Close(context.Background())

	digests, err := rec.ListSessions()
	if err != nil {
		return err
	}
	if len(digests) == 0 {
		fmt.Fprintln(out, "No turns recorded yet.")
		return nil
	}
	for _, d := range digests {
		model := d.Model
		if model == "" {
			model = "?"
		}
		fmt.Fprintf(out, "%-22s %-9s %3d turn%s  %s\n",
			d.SessionID, humanizeSince(time.Since(d.Last)), d.Turns, plural(d.Turns), model)
	}
	return nil
}

func runTranscriptSearch(cmd *cobra.Command, query string) error {
	rec, ok, err := openTranscriptRecorder(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !ok {
		transcriptHint(out)
		return nil
	}
	defer rec.Close(context.Background())

	session, _ := cmd.Flags().GetString("session")
	limit, _ := cmd.Flags().GetInt("limit")
	toolOnly, _ := cmd.Flags().GetBool("tool")
	hits, err := rec.Search(transcript.SearchOptions{
		Query:     query,
		SessionID: strings.TrimSpace(session),
		ToolOnly:  toolOnly,
		Limit:     limit,
	})
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Fprintf(out, "No turns match %q.\n", query)
		return nil
	}
	for _, h := range hits {
		fmt.Fprintf(out, "%s  %s\n", h.SessionID, humanizeSince(time.Since(h.Time)))
		if toolOnly {
			// A tool search matched a command/name, so lead with the tool text.
			if tl := previewLine(h.ToolText); tl != "" {
				fmt.Fprintf(out, "  tool: %s\n", tl)
			}
		}
		if u := previewLine(h.UserText); u != "" {
			fmt.Fprintf(out, "  user: %s\n", u)
		}
		if a := previewLine(h.AssistantText); a != "" {
			fmt.Fprintf(out, "  asst: %s\n", a)
		}
	}
	return nil
}

// previewLine collapses whitespace and truncates one transcript field for display.
func previewLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, transcriptPreviewRunes)
}
