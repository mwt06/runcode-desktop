// Package skill discovers and loads reusable skills. A skill is a directory
// holding a SKILL.md file whose frontmatter declares a name and description and
// whose body is detailed instructions for a workflow.
//
// Skills use progressive disclosure to stay cheap: only a compact catalog (each
// skill's name and description) is injected into the system prompt. The full body
// is disclosed on demand when the model calls the Skill tool with a skill name,
// so unused skills cost no context.
package skill

import (
	"fmt"
	"sort"
	"strings"
)

// Source identifies where a skill was discovered, used to attribute it in the
// catalog (project skills come from the workspace and carry a prompt-injection
// surface a user skill does not).
type Source string

const (
	// SourceUser is a skill from the per-user skills directory.
	SourceUser Source = "user"
	// SourceProject is a skill from the workspace's .runcode/skills directory.
	SourceProject Source = "project"
)

// Skill is one loaded skill.
type Skill struct {
	Name        string
	Description string
	Body        string
	Path        string // path to the SKILL.md file
	Source      Source
	Truncated   bool // body was longer than the size cap and was trimmed
}

// Set is an ordered, name-indexed collection of loaded skills. The first skill
// inserted for a given name wins, so callers add higher-priority sources (user)
// before lower-priority ones (project) to let a trusted user skill shadow a
// project skill of the same name rather than the reverse.
type Set struct {
	ordered []Skill
	byName  map[string]Skill
}

// NewSet builds a Set, dropping later duplicates by name and ordering the catalog
// by name for deterministic prompts.
func NewSet(skills []Skill) *Set {
	s := &Set{byName: make(map[string]Skill, len(skills))}
	for _, sk := range skills {
		if _, dup := s.byName[sk.Name]; dup {
			continue
		}
		s.byName[sk.Name] = sk
		s.ordered = append(s.ordered, sk)
	}
	sort.SliceStable(s.ordered, func(i, j int) bool { return s.ordered[i].Name < s.ordered[j].Name })
	return s
}

// Len reports how many skills are loaded.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ordered)
}

// All returns the loaded skills in catalog order.
func (s *Set) All() []Skill {
	if s == nil {
		return nil
	}
	return s.ordered
}

// Get looks up a skill by exact name.
func (s *Set) Get(name string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	sk, ok := s.byName[name]
	return sk, ok
}

// Names returns the loaded skill names in catalog order.
func (s *Set) Names() []string {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.ordered))
	for i, sk := range s.ordered {
		names[i] = sk.Name
	}
	return names
}

// Catalog renders the system-prompt section listing each skill's name and
// description (and a [project] tag for project-sourced skills). It returns the
// empty string when no skills are loaded so the prompt section is skipped.
func Catalog(s *Set) string {
	if s.Len() == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You have the following skills available. A skill is a reusable workflow with detailed instructions. When a skill is relevant to the task, call the Skill tool with its name to load the full instructions before proceeding. Skills tagged [project] come from the current workspace.\n")
	for _, sk := range s.All() {
		fmt.Fprintf(&b, "\nSkill: %s", sk.Name)
		if sk.Source == SourceProject {
			b.WriteString(" [project]")
		}
		fmt.Fprintf(&b, "\nDescription: %s\n", sk.Description)
	}
	return b.String()
}
