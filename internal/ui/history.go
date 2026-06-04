package ui

// promptHistory is a readline-style recall buffer for submitted inputs. It keeps
// every submitted line (prompts and slash commands) in order and lets the user
// walk backward (older) and forward (newer) through them, preserving the live
// draft they were editing before navigation started.
//
// The zero value is ready to use. It is intentionally decoupled from the input
// widget: callers pass the current input text in and apply the returned text to
// the widget, so the buffer is trivially unit-testable on its own.
type promptHistory struct {
	entries []string
	// pos is the cursor into entries during navigation. pos == len(entries)
	// means "on the live draft" (not currently recalling history).
	pos int
	// draft holds the live input saved when navigation first leaves the live
	// line, so stepping back down restores what the user was typing.
	draft string
}

// add records a submitted line and resets navigation to the live line.
// Empty lines are ignored, and a line identical to the most recent entry is not
// duplicated (matching shell history behavior).
func (h *promptHistory) add(text string) {
	defer func() {
		h.pos = len(h.entries)
		h.draft = ""
	}()
	if text == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == text {
		return
	}
	h.entries = append(h.entries, text)
}

// older returns the previous (older) entry to display. The first time navigation
// leaves the live line, current is saved as the draft. ok is false when there is
// no history at all. At the oldest entry it stays put and keeps returning it.
func (h *promptHistory) older(current string) (text string, ok bool) {
	if len(h.entries) == 0 {
		return "", false
	}
	if h.pos >= len(h.entries) {
		h.draft = current
		h.pos = len(h.entries)
	}
	if h.pos > 0 {
		h.pos--
	}
	return h.entries[h.pos], true
}

// newer returns the next (newer) entry, or the saved draft when stepping past the
// most recent entry back to the live line. ok is false when already on the live
// line (nothing newer to show).
func (h *promptHistory) newer() (text string, ok bool) {
	if h.pos >= len(h.entries) {
		return "", false
	}
	h.pos++
	if h.pos >= len(h.entries) {
		h.pos = len(h.entries)
		return h.draft, true
	}
	return h.entries[h.pos], true
}

// reset returns navigation to the live line without recording anything, used
// when the input is cleared by other means (e.g. /clear).
func (h *promptHistory) reset() {
	h.pos = len(h.entries)
	h.draft = ""
}
