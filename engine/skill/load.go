package skill

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wt68/runcode/engine/toolpath"
)

const (
	// DefinitionFileName is the file that marks a directory as a skill.
	DefinitionFileName = "SKILL.md"
	// DefaultMaxBodyBytes bounds a single skill body so a runaway file cannot
	// dominate the context window.
	DefaultMaxBodyBytes = 64 * 1024
	// DefaultMaxSkills bounds how many skills are loaded across all roots so a
	// directory full of skills cannot blow up the catalog. Excess skills are
	// dropped with a recorded Problem rather than silently.
	DefaultMaxSkills = 100
)

// Root is a directory to scan for skills, tagged with the source attribution to
// apply to the skills found under it.
type Root struct {
	Dir    string
	Source Source
}

// LoadOptions controls skill discovery.
type LoadOptions struct {
	// Roots are scanned in order; earlier roots take precedence on name conflicts.
	Roots []Root
	// MaxBodyBytes caps a single skill body (DefaultMaxBodyBytes when <= 0).
	MaxBodyBytes int
	// MaxSkills caps the total skills loaded (DefaultMaxSkills when <= 0).
	MaxSkills int
}

// Problem records a skill that could not be loaded, with a sanitized reason. It
// names the directory rather than echoing file contents.
type Problem struct {
	Dir    string
	Reason string
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s", p.Dir, p.Reason) }

// Load discovers skills under each root: every immediate subdirectory that holds
// a SKILL.md becomes a skill. Loading is tolerant — a malformed or oversized
// skill is skipped with a Problem rather than failing the whole load, so a bad
// skill never breaks a session. A missing root is not a problem.
func Load(opts LoadOptions) (*Set, []Problem) {
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	maxSkills := opts.MaxSkills
	if maxSkills <= 0 {
		maxSkills = DefaultMaxSkills
	}

	var (
		skills   []Skill
		problems []Problem
		capped   bool
	)
	for _, root := range opts.Roots {
		dir, err := normalizeDir(root.Dir)
		if err != nil {
			continue // an unresolvable root is treated as absent
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, Problem{Dir: dir, Reason: "cannot read skills directory"})
			}
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillDir := filepath.Join(dir, entry.Name())
			candidate := filepath.Join(skillDir, DefinitionFileName)
			sk, ok, problem := loadSkill(dir, skillDir, candidate, root.Source, maxBody)
			if problem != nil {
				problems = append(problems, *problem)
				continue
			}
			if !ok {
				continue
			}
			if len(skills) >= maxSkills {
				capped = true
				continue
			}
			skills = append(skills, sk)
		}
	}
	if capped {
		problems = append(problems, Problem{Dir: "", Reason: fmt.Sprintf("more than %d skills found; extra skills were skipped", maxSkills)})
	}
	return NewSet(skills), problems
}

// loadSkill reads one candidate SKILL.md. It returns ok=false (no problem) when
// the directory simply is not a skill (no SKILL.md), and a Problem when a present
// SKILL.md cannot be used.
func loadSkill(root, skillDir, path string, source Source, maxBody int) (Skill, bool, *Problem) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Skill{}, false, nil // not a skill directory
		}
		return Skill{}, false, &Problem{Dir: skillDir, Reason: "cannot stat SKILL.md"}
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return Skill{}, false, &Problem{Dir: skillDir, Reason: "SKILL.md is not a regular file"}
	}
	within, err := toolpath.IsWithinResolved(root, path)
	if err != nil || !within {
		return Skill{}, false, &Problem{Dir: skillDir, Reason: "SKILL.md resolves outside the skills directory"}
	}

	file, err := os.Open(path)
	if err != nil {
		return Skill{}, false, &Problem{Dir: skillDir, Reason: "cannot open SKILL.md"}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBody)+1))
	if err != nil {
		return Skill{}, false, &Problem{Dir: skillDir, Reason: "cannot read SKILL.md"}
	}
	truncated := len(data) > maxBody
	if truncated {
		data = data[:maxBody]
	}

	p, err := parseSkill(string(data))
	if err != nil {
		return Skill{}, false, &Problem{Dir: skillDir, Reason: err.Error()}
	}
	return Skill{
		Name:        p.Name,
		Description: p.Description,
		Body:        p.Body,
		Path:        path,
		Source:      source,
		Truncated:   truncated,
	}, true, nil
}

func normalizeDir(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("empty directory")
	}
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	return abs, nil
}
