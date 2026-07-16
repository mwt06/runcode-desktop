package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wt68/runcode/engine/agent"
	"github.com/wt68/runcode/engine/internal/subagent"
	"github.com/wt68/runcode/engine/memory"
	"github.com/wt68/runcode/engine/settings"
	"github.com/wt68/runcode/engine/skill"
	"github.com/wt68/runcode/engine/tools/bash"
)

const (
	// ProjectRuncodeDir is the workspace-local runcode data directory.
	ProjectRuncodeDir = ".runcode"
	// skillsDirName is the per-source subdirectory holding skill folders.
	skillsDirName = "skills"
	// agentsDirName is the per-source subdirectory holding sub-agent files.
	agentsDirName = "agents"
	// MemoryFileName is the per-scope memory file under each convention directory.
	MemoryFileName = "memory.md"
)

// userConfigDir returns the per-user config root, or "" if it cannot be
// determined (in which case the user-level layer is simply skipped).
func userConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return dir
}

// shellInfo names the shell the Bash tool actually runs commands in, for the
// prompt's environment section, so the model writes commands for the right shell
// and path style (PowerShell + `D:\…` on Windows, bash + POSIX paths elsewhere).
func shellInfo() string {
	return bash.ShellName()
}

// skillRoots returns the convention skill directories in precedence order: the
// per-user directory first (trusted, so it shadows a same-named project skill),
// then the project directory under the workspace's .runcode.
func skillRoots(cwd, userConfigDir string) []skill.Root {
	var roots []skill.Root
	if userConfigDir != "" {
		roots = append(roots, skill.Root{
			Dir:    filepath.Join(userConfigDir, settings.AppDirName, skillsDirName),
			Source: skill.SourceUser,
		})
	}
	if cwd != "" {
		roots = append(roots, skill.Root{
			Dir:    filepath.Join(cwd, ProjectRuncodeDir, skillsDirName),
			Source: skill.SourceProject,
		})
	}
	return roots
}

// LoadSkills discovers skills from the convention directories. Loading is
// tolerant: a malformed skill is skipped and reported as a problem rather than
// failing session startup.
func LoadSkills(cwd, userConfigDir string) (*skill.Set, []skill.Problem) {
	return skill.Load(skill.LoadOptions{Roots: skillRoots(cwd, userConfigDir)})
}

// reportSkillProblems writes a bounded, sanitized warning for each skill that
// could not be loaded. These are warnings, not fatal errors.
func reportSkillProblems(warn io.Writer, problems []skill.Problem) {
	if len(problems) == 0 || warn == nil {
		return
	}
	for _, p := range problems {
		if p.Dir == "" {
			fmt.Fprintf(warn, "warning: skill loading: %s\n", p.Reason)
			continue
		}
		fmt.Fprintf(warn, "warning: skill %q skipped: %s\n", filepath.Base(p.Dir), p.Reason)
	}
}

// agentRoots returns the convention sub-agent directories in precedence order:
// the per-user directory first (trusted, so it shadows a same-named project
// agent), then the project directory under the workspace's .runcode.
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
			Dir:    filepath.Join(cwd, ProjectRuncodeDir, agentsDirName),
			Source: agent.SourceProject,
		})
	}
	return roots
}

// BuiltinAgents returns the engine's built-in sub-agent definitions, so a host
// can list them (e.g. the desktop's agents page) without reaching into the
// engine's internals.
func BuiltinAgents() []agent.Agent {
	return subagent.BuiltinAgents()
}

// LoadAgents discovers sub-agents from the convention directories and merges them
// with the built-ins. Discovered definitions come first and built-ins last, so —
// because NewSet keeps the first agent seen for a name — the precedence is
// user > project > builtin. Loading is tolerant.
func LoadAgents(cwd, userConfigDir string) (*agent.Set, []agent.Problem) {
	discovered, problems := agent.Load(agent.LoadOptions{Roots: agentRoots(cwd, userConfigDir)})
	merged := append(discovered.All(), subagent.BuiltinAgents()...)
	return agent.NewSet(merged), problems
}

// reportAgentProblems writes a bounded, sanitized warning for each sub-agent that
// could not be loaded. These are warnings, not fatal errors.
func reportAgentProblems(warn io.Writer, problems []agent.Problem) {
	if len(problems) == 0 || warn == nil {
		return
	}
	for _, p := range problems {
		if p.Path == "" {
			fmt.Fprintf(warn, "warning: sub-agent loading: %s\n", p.Reason)
			continue
		}
		fmt.Fprintf(warn, "warning: sub-agent %q skipped: %s\n", filepath.Base(p.Path), p.Reason)
	}
}

// MemoryStore returns the two-scope memory store: user memory at
// <userConfigDir>/runcode/memory.md and project memory at
// <workspace>/.runcode/memory.md. A scope whose root is unavailable gets an empty
// path, which disables it. The store is process-shared per path pair (see
// memory.Shared), so every session over the same memory files serializes its
// appends on one lock.
func MemoryStore(cwd, userConfigDir string) *memory.Store {
	var userPath, projectPath string
	if userConfigDir != "" {
		userPath = filepath.Join(userConfigDir, settings.AppDirName, MemoryFileName)
	}
	if cwd != "" {
		projectPath = filepath.Join(cwd, ProjectRuncodeDir, MemoryFileName)
	}
	return memory.Shared(memory.Options{UserPath: userPath, ProjectPath: projectPath})
}
