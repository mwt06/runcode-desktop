package main

import (
	"fmt"
	"path/filepath"

	"github.com/wt68/runcode/internal/persistence/settings"
	"github.com/wt68/runcode/pkg/skill"
)

const (
	// skillsDirName is the per-source subdirectory holding skill folders.
	skillsDirName = "skills"
	// projectRuncodeDir is the workspace-local runcode data directory.
	projectRuncodeDir = ".runcode"
)

// skillRoots returns the convention skill directories in precedence order: the
// per-user directory first (trusted, so it shadows a same-named project skill),
// then the project directory under the workspace's .runcode. A skill is a
// subdirectory of one of these holding a SKILL.md.
//
// User skills live at <userConfigDir>/runcode/skills; project skills at
// <workspace>/.runcode/skills. An empty userConfigDir (undeterminable) drops the
// user layer, like the settings loader.
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
			Dir:    filepath.Join(cwd, projectRuncodeDir, skillsDirName),
			Source: skill.SourceProject,
		})
	}
	return roots
}

// loadSkills discovers skills from the convention directories. Loading is
// tolerant: a malformed skill is skipped and reported as a problem rather than
// failing session startup.
func loadSkills(cwd, userConfigDir string) (*skill.Set, []skill.Problem) {
	return skill.Load(skill.LoadOptions{Roots: skillRoots(cwd, userConfigDir)})
}

// reportSkillProblems writes a bounded, sanitized warning for each skill that
// could not be loaded. Like MCP startup, these are warnings, not fatal errors.
func reportSkillProblems(runtime chatIO, problems []skill.Problem) {
	if len(problems) == 0 || runtime.Err == nil {
		return
	}
	for _, p := range problems {
		if p.Dir == "" {
			fmt.Fprintf(runtime.Err, "warning: skill loading: %s\n", p.Reason)
			continue
		}
		fmt.Fprintf(runtime.Err, "warning: skill %q skipped: %s\n", filepath.Base(p.Dir), p.Reason)
	}
}
