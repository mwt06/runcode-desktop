package command

import "strings"

// maxNameLen bounds a command name so it stays a sane slash-command token.
const maxNameLen = 64

// utf8BOM is the byte-order mark some Windows editors prepend; stripping it keeps
// the frontmatter check from failing on an otherwise-valid file.
var utf8BOM = string([]byte{0xEF, 0xBB, 0xBF})

// parsed is the result of reading a command file. The name comes from the file
// name, not the frontmatter.
type parsed struct {
	Description  string
	ArgumentHint string
	Body         string
}

// parseCommand reads a command document. Frontmatter (a leading "---" block) is
// optional: when present its description and argument-hint keys are read; when
// absent the whole file is the body. Unknown keys are ignored so the format can
// grow.
func parseCommand(content string) parsed {
	content = strings.TrimPrefix(content, utf8BOM)
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return parsed{Body: strings.TrimLeft(normalized, "\n")}
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		// An unterminated frontmatter block is treated as plain body.
		return parsed{Body: strings.TrimLeft(normalized, "\n")}
	}

	var p parsed
	for _, line := range lines[1:closing] {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "description":
			p.Description = value
		case "argument-hint", "argument_hint":
			p.ArgumentHint = value
		}
	}
	p.Body = strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\n")
	return p
}

// validName allows name-safe characters only (matching skills/agents), so a
// command name round-trips cleanly as a slash-command token.
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
