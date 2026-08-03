// Package skilltool is the desktop's Skill tool: the engine's built-in discloser
// plus an announcement to the UI. Disclosure itself is unchanged — the model gets
// byte-for-byte what the engine would have given it — but each successful load
// also emits a progress event carrying the skill's description, scope and
// directory, which the chat renders as a card.
//
// Without it a skill load shows up as a bare "加载技能" row: the tool call's
// arguments hold only a name, and the result text is the instructions themselves
// (never shown), so the UI has nothing to say about what the model just picked up.
// That metadata lives in the loaded skill set, which is exactly what the engine
// hands this tool through SetSet.
//
// It is registered only in the desktop, via engine.Options.SkillTool — the same
// arrangement as open_preview and ReadOffice through ExtraTools, except that a
// Skill tool replaces the built-in one rather than joining it (tool names are
// unique per session). The engine still owns which skills exist.
package skilltool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/skill"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Tool is the desktop's Skill tool. It embeds the engine's implementation, so
// the name, schema, description, disclosure text and result retention are the
// built-in ones and stay in step with the engine; only Run is extended.
type Tool struct {
	*skill.Tool

	// mu guards set, the tool's own view of the current skills. The embedded tool
	// keeps its set private, and Run may execute on parallel goroutines (loading a
	// skill is concurrency-safe), so the metadata lookup needs its own copy.
	mu  sync.RWMutex
	set *skill.Set
}

// New returns the desktop's Skill tool. It starts empty; the engine installs the
// session's skills through SetSet during assembly and after every reload.
func New() *Tool {
	return &Tool{Tool: skill.NewTool(skill.NewSet(nil))}
}

// SetSet records the engine's current skill set for the card's metadata and
// forwards it to the embedded tool, which resolves the model's requests against
// it. Both must see the same set or the card would describe a different skill
// than the one disclosed.
func (t *Tool) SetSet(set *skill.Set) {
	t.mu.Lock()
	t.set = set
	t.mu.Unlock()
	t.Tool.SetSet(set)
}

func (t *Tool) currentSet() *skill.Set {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.set
}

// Run discloses the skill through the engine's implementation and, when that
// succeeded, announces it to the UI. A failed or unknown-skill call announces
// nothing: the row already reports the error, and there is no skill to describe.
func (t *Tool) Run(ctx context.Context, raw json.RawMessage, tctx *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	result, err := t.Tool.Run(ctx, raw, tctx, out)
	if err != nil || result.IsError {
		return result, err
	}
	t.announce(raw, out)
	return result, nil
}

// announce emits the loaded skill's metadata for the chat card. The event is
// dropped rather than blocked on a full channel — a card is cosmetic and must
// never stall the turn that produced it. The executor's forwarder fills in the
// tool name and tool-use id, which is what binds this payload to the right card.
func (t *Tool) announce(raw json.RawMessage, out chan<- tool.Event) {
	if out == nil {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return
	}
	loaded, ok := t.currentSet().Get(strings.TrimSpace(in.Name))
	if !ok {
		return
	}
	select {
	case out <- tool.Event{
		Type:    tool.EventTypeProgress,
		Message: "加载技能 " + loaded.Name,
		Data:    skillLoadDTO(loaded),
	}:
	default:
	}
}

// skillLoadDTO maps a loaded skill to its card payload. Dir is the skill's own
// folder (the SKILL.md's parent) — the anchor its bundled scripts and references
// are named relative to, and the only path worth showing.
func skillLoadDTO(loaded skill.Skill) protocol.SkillLoad {
	dir := ""
	if path := strings.TrimSpace(loaded.Path); path != "" {
		dir = filepath.ToSlash(filepath.Dir(path))
	}
	return protocol.SkillLoad{
		Name:        loaded.Name,
		Description: loaded.Description,
		Source:      string(loaded.Source),
		Dir:         dir,
		Truncated:   loaded.Truncated,
	}
}
