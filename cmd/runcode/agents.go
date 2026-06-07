package main

import (
	"fmt"
	"path/filepath"

	"github.com/wt68/runcode/internal/persistence/settings"
	"github.com/wt68/runcode/internal/subagent"
	"github.com/wt68/runcode/pkg/agent"
)

// agentsDirName is the per-source subdirectory holding sub-agent definition files.
const agentsDirName = "agents"

// agentRoots returns the convention sub-agent directories in precedence order: the
// per-user directory first (trusted, so it shadows a same-named project agent),
// then the project directory under the workspace's .runcode.
//
// User agents live at <userConfigDir>/runcode/agents; project agents at
// <workspace>/.runcode/agents. An empty userConfigDir (undeterminable) drops the
// user layer, like the settings loader.
func agentRoots(cwd, userConfigDir string) []agent.Root {
	var roots []agent.Root
	if userConfigDir != "" {
		roots = append(roots, agent.Root{
			Dir:    filepath.Join(userConfigDir, settings.AppDirName, agentsDirName),
			Source: agent.SourceUser,
		})
	}
	if cwd != "" {
		roots = append(roots, agent.Root{
			Dir:    filepath.Join(cwd, projectRuncodeDir, agentsDirName),
			Source: agent.SourceProject,
		})
	}
	return roots
}

// loadAgents discovers sub-agents from the convention directories and merges them
// with the built-in agents. Discovered definitions come first and built-ins last,
// so — because NewSet keeps the first agent seen for a name — the precedence is
// user > project > builtin: a user or project definition shadows a same-named
// builtin, which therefore acts only as a fallback. Loading is tolerant: a
// malformed definition is skipped and reported as a problem rather than failing
// startup.
func loadAgents(cwd, userConfigDir string) (*agent.Set, []agent.Problem) {
	discovered, problems := agent.Load(agent.LoadOptions{Roots: agentRoots(cwd, userConfigDir)})
	merged := append(discovered.All(), subagent.BuiltinAgents()...)
	return agent.NewSet(merged), problems
}

// reportAgentProblems writes a bounded, sanitized warning for each sub-agent that
// could not be loaded. Like MCP startup and skills, these are warnings, not fatal
// errors.
func reportAgentProblems(runtime chatIO, problems []agent.Problem) {
	if len(problems) == 0 || runtime.Err == nil {
		return
	}
	for _, p := range problems {
		if p.Path == "" {
			fmt.Fprintf(runtime.Err, "warning: sub-agent loading: %s\n", p.Reason)
			continue
		}
		fmt.Fprintf(runtime.Err, "warning: sub-agent %q skipped: %s\n", filepath.Base(p.Path), p.Reason)
	}
}
