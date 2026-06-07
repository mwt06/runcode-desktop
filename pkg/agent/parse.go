package agent

import (
	"errors"
	"strings"
)

// maxNameLen bounds an agent name so the catalog, tool argument, and model-facing
// references stay sane.
const maxNameLen = 64

// utf8BOM is the byte-order mark some Windows editors prepend; stripping it keeps
// the frontmatter check from failing on an otherwise-valid definition. It is built
// from bytes so this source file stays BOM-free.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

var (
	// errNoFrontmatter is returned when a definition lacks the leading "---" /
	// trailing "---" frontmatter block.
	errNoFrontmatter = errors.New("missing frontmatter")
	// errMissingName is returned when the frontmatter has no name.
	errMissingName = errors.New("frontmatter missing name")
	// errMissingDescription is returned when the frontmatter has no description.
	errMissingDescription = errors.New("frontmatter missing description")
	// errMissingPrompt is returned when the body (the system prompt) is empty.
	errMissingPrompt = errors.New("agent definition has no prompt body")
	// errInvalidName is returned when an agent name is not name-safe.
	errInvalidName = errors.New("invalid agent name (use letters, digits, '-' or '_')")
)

// parsed is the result of reading an agent definition file.
type parsed struct {
	Name        string
	Description string
	Tools       []string
	Model       string
	Prompt      string
}

// parseAgent reads an agent definition: a YAML-style frontmatter block delimited
// by lines containing only "---", carrying name, description, and optional tools
// and model keys, followed by the system-prompt body. Only the keys runcode needs
// are read; unknown keys are ignored so the format can grow without breaking older
// parsers.
//
// The tools value is a comma- or whitespace-separated list of tool names; it is
// optional and, when omitted, the agent inherits every tool the parent can
// delegate. The model value is optional and overrides only the model name.
func parseAgent(content string) (parsed, error) {
	// Tolerate a UTF-8 BOM that some Windows editors prepend, which would otherwise
	// make the first line fail the "---" frontmatter check.
	content = strings.TrimPrefix(content, utf8BOM)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return parsed{}, errNoFrontmatter
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return parsed{}, errNoFrontmatter
	}

	var p parsed
	for _, line := range lines[1:closing] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`) // tolerate quoted values
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			p.Name = value
		case "description":
			p.Description = value
		case "tools":
			p.Tools = parseToolList(value)
		case "model":
			p.Model = value
		}
	}

	if p.Name == "" {
		return parsed{}, errMissingName
	}
	if !validName(p.Name) {
		return parsed{}, errInvalidName
	}
	if p.Description == "" {
		return parsed{}, errMissingDescription
	}
	p.Prompt = strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\n")
	if strings.TrimSpace(p.Prompt) == "" {
		return parsed{}, errMissingPrompt
	}
	return p, nil
}

// parseToolList splits a tools frontmatter value on commas and whitespace,
// dropping empties. The result preserves order and may contain the "*" wildcard,
// which the agent policy interprets as inherit-all.
func parseToolList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})
	if len(fields) == 0 {
		return nil
	}
	tools := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f != "" {
			tools = append(tools, f)
		}
	}
	return tools
}

// validName mirrors the skill/MCP server-name rule: name-safe characters only,
// bounded length, so an agent name round-trips cleanly as a catalog key and tool
// argument.
func validName(name string) bool {
	if name == "" || len(name) > maxNameLen {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
