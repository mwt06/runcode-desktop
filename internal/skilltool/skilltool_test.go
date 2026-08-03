package skilltool

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/skill"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// drain collects the events a run emitted. The channel is buffered well past what
// one call produces, so nothing is dropped by the tool's non-blocking send.
func drain(events chan tool.Event) []tool.Event {
	close(events)
	var out []tool.Event
	for e := range events {
		out = append(out, e)
	}
	return out
}

func loadedSkill(dir string) skill.Skill {
	return skill.Skill{
		Name:        "dataviz",
		Description: "图表与数据可视化",
		Body:        "第一步：选图形。",
		Path:        filepath.Join(dir, "dataviz", skill.DefinitionFileName),
		Source:      skill.SourceUser,
	}
}

// The model gets the engine's disclosure unchanged, and the UI additionally gets
// the metadata the tool call itself never carries.
func TestRunDisclosesAndAnnounces(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	loaded := loadedSkill(dir)
	tl := New()
	tl.SetSet(skill.NewSet([]skill.Skill{loaded}))

	events := make(chan tool.Event, 4)
	result, err := tl.Run(context.Background(), json.RawMessage(`{"name":"dataviz"}`), nil, events)
	if err != nil {
		t.Fatalf("run skill tool: %v", err)
	}
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("result = %#v, want the skill body", result)
	}
	if !strings.Contains(result.Content[0].Text, loaded.Body) {
		t.Fatalf("result dropped the skill body: %q", result.Content[0].Text)
	}

	emitted := drain(events)
	if len(emitted) != 1 {
		t.Fatalf("emitted %d events, want exactly one announcement", len(emitted))
	}
	payload, ok := emitted[0].Data.(protocol.SkillLoad)
	if !ok {
		t.Fatalf("event data = %#v, want a SkillLoad payload", emitted[0].Data)
	}
	if payload.Name != "dataviz" || payload.Description != "图表与数据可视化" || payload.Source != string(skill.SourceUser) {
		t.Fatalf("payload = %#v, want the loaded skill's identity", payload)
	}
	if want := filepath.ToSlash(filepath.Join(dir, "dataviz")); payload.Dir != want {
		t.Fatalf("payload dir = %q, want the skill's own folder %q", payload.Dir, want)
	}
}

// An unknown skill is already reported on the row as an error; announcing a card
// for it would describe a skill that was never loaded.
func TestRunDoesNotAnnounceUnknownSkill(t *testing.T) {
	t.Parallel()

	tl := New()
	tl.SetSet(skill.NewSet([]skill.Skill{loadedSkill(t.TempDir())}))

	events := make(chan tool.Event, 4)
	result, err := tl.Run(context.Background(), json.RawMessage(`{"name":"nope"}`), nil, events)
	if err != nil {
		t.Fatalf("run skill tool: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v, want an is_error result for an unknown skill", result)
	}
	if emitted := drain(events); len(emitted) != 0 {
		t.Fatalf("emitted %#v, want no announcement", emitted)
	}
}

// SetSet has to reach the embedded tool too: the card and the disclosure must
// come from one set, or the model would be handed a skill the card misdescribes
// (or the reload would silently keep serving the previous body).
func TestSetSetReachesTheEmbeddedTool(t *testing.T) {
	t.Parallel()

	tl := New()
	tl.SetSet(skill.NewSet([]skill.Skill{{Name: "deploy", Description: "ship it", Body: "old body"}}))
	tl.SetSet(skill.NewSet([]skill.Skill{{Name: "deploy", Description: "ship it", Body: "new body"}}))

	events := make(chan tool.Event, 4)
	result, err := tl.Run(context.Background(), json.RawMessage(`{"name":"deploy"}`), nil, events)
	if err != nil {
		t.Fatalf("run skill tool: %v", err)
	}
	if got := result.Content[0].Text; got != "new body" {
		t.Fatalf("disclosed %q, want the body from the latest set", got)
	}
}

// A card must never hold up the turn that produced it: with nowhere to put the
// event, the run still returns the skill.
func TestRunSurvivesAFullOrAbsentEventChannel(t *testing.T) {
	t.Parallel()

	tl := New()
	tl.SetSet(skill.NewSet([]skill.Skill{{Name: "deploy", Description: "ship it", Body: "do the deploy"}}))

	for _, events := range []chan tool.Event{nil, make(chan tool.Event)} {
		result, err := tl.Run(context.Background(), json.RawMessage(`{"name":"deploy"}`), nil, events)
		if err != nil {
			t.Fatalf("run skill tool: %v", err)
		}
		if result.Content[0].Text != "do the deploy" {
			t.Fatalf("result = %#v, want the skill body", result)
		}
	}
}

// The engine only accepts a replacement that keeps the model-facing name, and the
// desktop's tool has to satisfy the port it is installed through.
func TestToolSatisfiesTheEnginePort(t *testing.T) {
	t.Parallel()

	var discloser skill.Discloser = New()
	if discloser.Name() != skill.ToolName {
		t.Fatalf("name = %q, want %q", discloser.Name(), skill.ToolName)
	}
}
