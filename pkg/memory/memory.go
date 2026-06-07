// Package memory persists facts the assistant learns across sessions. Unlike
// project context (a human-authored RUNCODE.md / CLAUDE.md checked into the repo),
// memory is runcode's own running notebook: stable user preferences, project
// conventions, and gotchas the model chooses to remember via the Remember tool.
//
// Memory lives in two scopes, each a Markdown file of one-line bullet entries:
//   - user:    <userConfigDir>/runcode/memory.md   — preferences across all projects
//   - project: <workspace>/.runcode/memory.md      — facts specific to this project
//
// Project memory sits under .runcode/, which is git-ignored, so it stays a local
// notebook rather than something committed to the repo. Only a compact rendering
// of the entries is injected into the system prompt.
package memory

import (
	"fmt"
	"strings"
)

// Scope identifies which memory file an entry belongs to.
type Scope string

const (
	// ScopeProject is the workspace-local memory (the default for Remember).
	ScopeProject Scope = "project"
	// ScopeUser is the per-user memory shared across projects.
	ScopeUser Scope = "user"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool {
	return s == ScopeProject || s == ScopeUser
}

// maxEntryLen bounds a single remembered fact so one entry cannot dominate the
// prompt or the file.
const maxEntryLen = 1000

// Loaded is the parsed contents of both memory scopes.
type Loaded struct {
	User      []string
	Project   []string
	Truncated bool // a scope file exceeded the byte cap and was trimmed
}

// Empty reports whether no memories are present.
func (l Loaded) Empty() bool {
	return len(l.User) == 0 && len(l.Project) == 0
}

// Count returns the total number of entries across both scopes.
func (l Loaded) Count() int {
	return len(l.User) + len(l.Project)
}

// Format renders the system-prompt section for the loaded memories, or "" when
// there are none so the section is skipped. The guidance frames the entries as
// established facts and tells the model how (and how sparingly) to add to them.
func Format(l Loaded) string {
	if l.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Memory — persistent notes you saved in earlier sessions. Treat them as established facts about the user and this project, and let them inform your work without restating them. When you learn a durable, reusable fact (a user preference, a project convention, a recurring gotcha), call the Remember tool to save it; keep such facts stable and general, not one-off details about the current task.")
	if len(l.User) > 0 {
		b.WriteString("\n\nUser memories (apply across projects):")
		writeEntries(&b, l.User)
	}
	if len(l.Project) > 0 {
		b.WriteString("\n\nProject memories (this workspace):")
		writeEntries(&b, l.Project)
	}
	if l.Truncated {
		b.WriteString("\n\n[memory truncated]")
	}
	return b.String()
}

func writeEntries(b *strings.Builder, entries []string) {
	for _, e := range entries {
		fmt.Fprintf(b, "\n- %s", e)
	}
}

// normalizeEntry trims a fact and collapses internal newlines to spaces so each
// memory stays a single bullet line. It returns the cleaned text and whether it is
// usable (non-empty). Over-long entries are truncated to the cap.
func normalizeEntry(fact string) (string, bool) {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return "", false
	}
	fact = strings.Join(strings.Fields(fact), " ")
	if len(fact) > maxEntryLen {
		fact = strings.TrimSpace(fact[:maxEntryLen])
	}
	return fact, fact != ""
}

// parseEntries extracts memory bullet lines ("- fact") from a file's content,
// ignoring blank lines, headings, and any other non-bullet text so a hand-edited
// file with a title still parses. Order is preserved.
func parseEntries(content string) []string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var entries []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		entry := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		if entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}
