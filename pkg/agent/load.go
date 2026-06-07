package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
)

const (
	// DefinitionFileExt is the extension that marks a file as an agent definition.
	DefinitionFileExt = ".md"
	// readmeFileName is skipped during discovery so a README documenting an agents
	// directory is treated as documentation, not a malformed agent definition.
	readmeFileName = "README.md"
	// DefaultMaxBodyBytes bounds a single agent prompt so a runaway file cannot
	// dominate the sub-agent's context window.
	DefaultMaxBodyBytes = 64 * 1024
	// DefaultMaxAgents bounds how many agents are loaded across all roots so a
	// directory full of definitions cannot blow up the catalog. Excess agents are
	// dropped with a recorded Problem rather than silently.
	DefaultMaxAgents = 100
)

// Root is a directory to scan for agent definitions, tagged with the source
// attribution to apply to the agents found under it.
type Root struct {
	Dir    string
	Source Source
}

// LoadOptions controls agent discovery.
type LoadOptions struct {
	// Roots are scanned in order; earlier roots take precedence on name conflicts.
	Roots []Root
	// MaxBodyBytes caps a single agent prompt body (DefaultMaxBodyBytes when <= 0).
	MaxBodyBytes int
	// MaxAgents caps the total agents loaded (DefaultMaxAgents when <= 0).
	MaxAgents int
}

// Problem records an agent that could not be loaded, with a sanitized reason. It
// names the file rather than echoing its contents.
type Problem struct {
	Path   string
	Reason string
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s", p.Path, p.Reason) }

// Load discovers agent definitions under each root: every immediate ".md" file
// (except a README.md, treated as directory documentation) becomes an agent.
// Loading is tolerant — a malformed or oversized definition is
// skipped with a Problem rather than failing the whole load, so a bad agent never
// breaks a session. A missing root is not a problem.
//
// builtins are seeded first so a definition file can shadow a same-named builtin
// only if Load is called with builtins appended after; callers pass builtins via a
// dedicated root-free path (see NewSet ordering). Load itself only reads disk.
func Load(opts LoadOptions) (*Set, []Problem) {
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	maxAgents := opts.MaxAgents
	if maxAgents <= 0 {
		maxAgents = DefaultMaxAgents
	}

	var (
		agents   []Agent
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
				problems = append(problems, Problem{Path: dir, Reason: "cannot read agents directory"})
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if !strings.EqualFold(filepath.Ext(entry.Name()), DefinitionFileExt) {
				continue
			}
			if strings.EqualFold(entry.Name(), readmeFileName) {
				continue // documentation for the directory, not an agent
			}
			path := filepath.Join(dir, entry.Name())
			a, ok, problem := loadAgent(dir, path, root.Source, maxBody)
			if problem != nil {
				problems = append(problems, *problem)
				continue
			}
			if !ok {
				continue
			}
			if len(agents) >= maxAgents {
				capped = true
				continue
			}
			agents = append(agents, a)
		}
	}
	if capped {
		problems = append(problems, Problem{Reason: fmt.Sprintf("more than %d agents found; extra agents were skipped", maxAgents)})
	}
	return NewSet(agents), problems
}

// loadAgent reads one candidate definition file. It returns ok=false (no problem)
// only for transient non-regular entries; a present-but-unusable file yields a
// Problem.
func loadAgent(root, path string, source Source, maxBody int) (Agent, bool, *Problem) {
	info, err := os.Lstat(path)
	if err != nil {
		return Agent{}, false, &Problem{Path: path, Reason: "cannot stat definition"}
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return Agent{}, false, &Problem{Path: path, Reason: "definition is not a regular file"}
	}
	within, err := toolpath.IsWithinResolved(root, path)
	if err != nil || !within {
		return Agent{}, false, &Problem{Path: path, Reason: "definition resolves outside the agents directory"}
	}

	file, err := os.Open(path)
	if err != nil {
		return Agent{}, false, &Problem{Path: path, Reason: "cannot open definition"}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBody)+1))
	if err != nil {
		return Agent{}, false, &Problem{Path: path, Reason: "cannot read definition"}
	}
	truncated := len(data) > maxBody
	if truncated {
		data = data[:maxBody]
	}

	p, err := parseAgent(string(data))
	if err != nil {
		return Agent{}, false, &Problem{Path: path, Reason: err.Error()}
	}
	return Agent{
		Name:        p.Name,
		Description: p.Description,
		Tools:       p.Tools,
		Model:       p.Model,
		Prompt:      p.Prompt,
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
