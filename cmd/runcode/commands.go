package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/wt68/runcode/internal/command"
	"github.com/wt68/runcode/internal/ui"
	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
)

// commandsDirName is the per-source subdirectory holding custom command files.
const commandsDirName = "commands"

// commandRoots returns the convention command directories in precedence order:
// the per-user directory first (trusted, so it shadows a same-named project
// command), then the project directory under the workspace's .runcode. A command
// is a *.md file in one of these directories.
func commandRoots(cwd, userConfigDir string) []command.Root {
	var roots []command.Root
	if userConfigDir != "" {
		roots = append(roots, command.Root{
			Dir:    filepath.Join(userConfigDir, settings.AppDirName, commandsDirName),
			Source: command.SourceUser,
		})
	}
	if cwd != "" {
		roots = append(roots, command.Root{
			Dir:    filepath.Join(cwd, projectRuncodeDir, commandsDirName),
			Source: command.SourceProject,
		})
	}
	return roots
}

// loadCustomCommands discovers custom slash commands from the convention
// directories. Loading is tolerant: a malformed command file is skipped and
// reported rather than failing startup.
func loadCustomCommands(cwd, userConfigDir string) (*command.Set, []command.Problem) {
	return command.Load(command.LoadOptions{Roots: commandRoots(cwd, userConfigDir)})
}

// uiCustomCommands adapts a loaded command set to the UI's slash-command specs.
func uiCustomCommands(set *command.Set) []ui.CustomCommand {
	if set == nil {
		return nil
	}
	out := make([]ui.CustomCommand, 0, set.Len())
	for _, c := range set.All() {
		summary := c.Description
		if c.ArgumentHint != "" {
			summary += " (" + c.ArgumentHint + ")"
		}
		out = append(out, ui.CustomCommand{Name: c.Name, Summary: summary, Body: c.Body})
	}
	return out
}

// reportCommandProblems writes a bounded warning for each command that could not
// be loaded. Like skills, these are warnings, not fatal errors.
func reportCommandProblems(w io.Writer, problems []command.Problem) {
	if len(problems) == 0 || w == nil {
		return
	}
	for _, p := range problems {
		if p.Path == "" {
			_, _ = fmt.Fprintf(w, "warning: command loading: %s\n", p.Reason)
			continue
		}
		_, _ = fmt.Fprintf(w, "warning: command %q skipped: %s\n", filepath.Base(p.Path), p.Reason)
	}
}
