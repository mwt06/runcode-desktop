package skill

import (
	"errors"
	"strings"
)

// maxNameLen bounds a skill name so the catalog and tool argument stay sane.
const maxNameLen = 64

// utf8BOM is the byte-order mark some Windows editors prepend; stripping it keeps
// the frontmatter check from failing on an otherwise-valid SKILL.md. It is built
// from bytes so this source file stays BOM-free.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

var (
	// errNoFrontmatter is returned when a SKILL.md lacks the leading "---" /
	// trailing "---" frontmatter block.
	errNoFrontmatter = errors.New("missing frontmatter")
	// errMissingName is returned when the frontmatter has no name.
	errMissingName = errors.New("frontmatter missing name")
	// errMissingDescription is returned when the frontmatter has no description.
	errMissingDescription = errors.New("frontmatter missing description")
	// errInvalidName is returned when a skill name is not name-safe.
	errInvalidName = errors.New("invalid skill name (use letters, digits, '-' or '_')")
)

// parsed is the result of reading a SKILL.md file.
type parsed struct {
	Name        string
	Description string
	Body        string
}

// parseSkill reads a SKILL.md document: a YAML-style frontmatter block delimited
// by lines containing only "---", carrying name and description, followed by the
// instruction body. Only the two keys runcode needs are read; other frontmatter
// keys are ignored so the format can grow without breaking older parsers.
func parseSkill(content string) (parsed, error) {
	// Tolerate a UTF-8 BOM that some Windows editors prepend, which would
	// otherwise make the first line fail the "---" frontmatter check.
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
	p.Body = strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\n")
	return p, nil
}

// validName mirrors the MCP server-name rule: name-safe characters only, bounded
// length, so a skill name round-trips cleanly as a catalog key and tool argument.
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
