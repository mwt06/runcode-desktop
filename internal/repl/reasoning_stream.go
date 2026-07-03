package repl

import (
	"encoding/json"
	"strings"
)

// This file adds progressive ("streaming") rendering of a scenario's structured
// analysis: as the model's output arrives token by token, the partial content of
// each step is extracted and surfaced so the UI fills the thinking card live,
// rather than popping the whole card in at once. Two wire formats are handled:
//   - pre-turn: a flat JSON object keyed by step, {"symptom":"...", ...}
//   - in-turn:  the Analyze tool arguments, {"steps":[{"key":"...","content":"..."}]}

// analysisInputFrom builds the Analyze event input (method + protocol-ordered,
// labeled steps) from a key→content map, plus a signature that changes only when
// the rendered content changes — so a caller can suppress no-op progress events.
func (p ReasoningProtocol) analysisInputFrom(content map[string]string) (json.RawMessage, string) {
	steps := make([]map[string]string, 0, len(p.Steps))
	var sig strings.Builder
	for _, step := range p.Steps {
		val := strings.ToValidUTF8(strings.TrimSpace(content[step.Key]), "")
		steps = append(steps, map[string]string{"key": step.Key, "label": step.Label, "content": val})
		sig.WriteString(val)
		sig.WriteByte('\x1f')
	}
	b, err := json.Marshal(map[string]any{"method": p.Method, "steps": steps})
	if err != nil {
		return nil, ""
	}
	return b, sig.String()
}

// partialPreTurnContent extracts each step's (possibly still-streaming) content
// from the pre-turn flat JSON object buffer.
func (p ReasoningProtocol) partialPreTurnContent(buf string) map[string]string {
	out := make(map[string]string, len(p.Steps))
	for _, step := range p.Steps {
		out[step.Key], _ = partialJSONStringField(buf, step.Key)
	}
	return out
}

// partialInTurnContent extracts each step's content from the in-turn Analyze tool
// arguments buffer, tolerating a trailing incomplete array element.
func partialInTurnContent(buf string) map[string]string {
	out := map[string]string{}
	si := strings.Index(buf, "\"steps\"")
	if si < 0 {
		return out
	}
	rest := buf[si:]
	lb := strings.IndexByte(rest, '[')
	if lb < 0 {
		return out
	}
	rest = rest[lb+1:]
	flush := func(obj string) {
		key, _ := partialJSONStringField(obj, "key")
		if key == "" {
			return
		}
		out[key], _ = partialJSONStringField(obj, "content")
	}
	depth, start := 0, -1
	inStr, esc := false, false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					flush(rest[start : i+1])
					start = -1
				}
			}
		case ']':
			if depth == 0 {
				return out
			}
		}
	}
	if depth > 0 && start >= 0 {
		flush(rest[start:]) // buffer ended mid-element
	}
	return out
}

// partialJSONStringField reads the string value of key from a possibly-truncated
// JSON object buffer, returning the decoded value so far and whether its closing
// quote was seen. A missing key, or a value not yet started, yields ("", false).
func partialJSONStringField(buf, key string) (string, bool) {
	i := strings.Index(buf, "\""+key+"\"")
	if i < 0 {
		return "", false
	}
	rest := buf[i+len(key)+2:]
	j := 0
	for j < len(rest) {
		if c := rest[j]; c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ':' {
			j++
			continue
		}
		break
	}
	if j >= len(rest) || rest[j] != '"' {
		return "", false
	}
	j++
	var b strings.Builder
	for j < len(rest) {
		c := rest[j]
		if c == '\\' {
			if j+1 >= len(rest) {
				return b.String(), false // escape spans the buffer boundary
			}
			switch rest[j+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(rest[j+1]) // ", \, /, and best-effort for others
			}
			j += 2
			continue
		}
		if c == '"' {
			return b.String(), true
		}
		b.WriteByte(c)
		j++
	}
	return b.String(), false
}
