package sessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wt68/runcode/pkg/llm"
)

// previewRunes bounds how much of a user prompt is kept for a list preview.
const previewRunes = 80

// Info is lightweight metadata about a saved session, for browse/resume UIs. It
// is cheap to compute (a single streaming pass over the file) and carries no full
// history; callers that need the conversation use LoadHistory.
type Info struct {
	ID        string    // session id (the file name without .jsonl)
	ModTime   time.Time // last write time, used as the recency key
	SizeBytes int64     // on-disk size of the history file
	Messages  int       // total persisted messages
	Turns     int       // user-prompt messages (an approximation of conversation turns)
	FirstUser string    // first user prompt, collapsed and truncated for display
	LastUser  string    // most recent user prompt, collapsed and truncated for display
}

// List returns metadata for every saved session in the workspace, newest first.
// A workspace with no sessions directory returns (nil, nil). A single unreadable
// or corrupt file is skipped rather than failing the whole listing, so one bad
// file cannot hide the rest; use Describe to surface a specific file's error.
func List(workspace string) ([]Info, error) {
	dir, err := sessionsDir(workspace)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions directory: %w", err)
	}
	var infos []Info
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		info, err := describePath(filepath.Join(dir, entry.Name()), id)
		if err != nil {
			continue // skip unreadable/corrupt files; Describe reports them per-id
		}
		infos = append(infos, info)
	}
	sort.SliceStable(infos, func(i, j int) bool {
		return infos[i].ModTime.After(infos[j].ModTime)
	})
	return infos, nil
}

// Describe returns metadata for a single saved session. A missing or corrupt file
// is returned as an error (unlike List, which skips it) so a targeted lookup is
// not silently empty.
func Describe(workspace string, id string) (Info, error) {
	path, err := sessionFilePath(workspace, id)
	if err != nil {
		return Info{}, err
	}
	return describePath(path, id)
}

// describePath streams one history file and extracts its metadata without
// reconstructing the whole conversation in memory.
func describePath(path string, id string) (Info, error) {
	file, err := os.Open(path)
	if err != nil {
		return Info{}, fmt.Errorf("open session %q: %w", id, err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return Info{}, fmt.Errorf("stat session %q: %w", id, err)
	}
	info := Info{ID: id, ModTime: stat.ModTime(), SizeBytes: stat.Size()}

	// Shares scanHistory's torn-trailing-line tolerance with LoadHistory, so a
	// session's metadata stays describable after a crash-truncated final write.
	if err := scanHistory(file, func(message llm.Message) error {
		info.Messages++
		if prompt := userPrompt(message); prompt != "" {
			info.Turns++
			if info.FirstUser == "" {
				info.FirstUser = prompt
			}
			info.LastUser = prompt
		}
		return nil
	}); err != nil {
		return Info{}, fmt.Errorf("parse session %q: %w", id, err)
	}
	return info, nil
}

// userPrompt returns a display-ready preview of a message's user text, or "" if
// the message is not a user prompt (e.g. an assistant message, or a user message
// carrying only tool_result blocks, which have no text content).
func userPrompt(message llm.Message) string {
	if message.Role != llm.RoleUser {
		return ""
	}
	return previewText(llm.TextContent(message))
}

// previewText collapses whitespace and truncates to previewRunes on a rune
// boundary so a preview is a single tidy line regardless of the original text.
func previewText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= previewRunes {
		return s
	}
	return string(runes[:previewRunes]) + "…"
}
