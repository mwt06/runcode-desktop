// Package plantool implements the plan_write tool: in plan mode the model records
// its planning output one stage at a time — 需求理解 / 方案设计 / 方案审查 — and the
// desktop turns those records into an editable, approvable checklist.
//
// The pipeline is enforced here rather than by prompting, and driven by the tool's
// own result text: each accepted call answers with what the next stage requires, so
// the ReAct loop walks the stages by itself inside a single turn. An out-of-order
// call is rejected with the stage that is actually due, which is what keeps "按步骤
// 执行" a property of the system instead of a hope about the model.
//
// It is registered only in the desktop (via engine.Options.ExtraTools), never the
// CLI: the checklist only means something where there is a UI to edit and approve it.
// 名字带 tool 后缀与 internal/desktop/plan.go(阶段机与命令)区分:那是外壳侧的状态,
// 这里是给模型用的写入口。
package plantool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/protocol"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// Name is the wire name the model calls.
const Name = "plan_write"

const (
	// maxSteps bounds one plan. A plan past this is not a plan, and the approval UI
	// stops being reviewable long before it.
	maxSteps = 40
	// maxNotes bounds each of the risks / questions / review-notes / non-goals lists.
	maxNotes = 20
	// maxTitleChars and maxTextChars bound a single field, so one runaway generation
	// cannot push a multi-megabyte document into the session's plan file.
	maxTitleChars = 200
	maxTextChars  = 4000
)

// Store is the desktop side of the tool: it owns the plan document, the stage the
// run is on, and whether plan mode is even active. The tool holds no state — two
// sessions must not share a plan, and the document has to outlive the process (the
// approval gate can be crossed after a restart), both of which are the shell's job.
type Store interface {
	// RecordStage stores one stage's output and returns the instruction the model
	// gets back as the tool result — normally "now do the next stage", and after the
	// last one "stop and wait for the user's approval".
	//
	// The error is the gate: plan mode off, or a stage out of order. Its message goes
	// to the model as an is_error result, so it must say what to do instead.
	RecordStage(stage string, doc protocol.PlanDoc) (next string, err error)
}

type inputStep struct {
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Files  []string `json:"files,omitempty"`
}

type input struct {
	Stage       string      `json:"stage"`
	Goal        string      `json:"goal,omitempty"`
	NonGoals    []string    `json:"nonGoals,omitempty"`
	Title       string      `json:"title,omitempty"`
	Steps       []inputStep `json:"steps,omitempty"`
	Risks       []string    `json:"risks,omitempty"`
	Questions   []string    `json:"questions,omitempty"`
	ReviewNotes []string    `json:"reviewNotes,omitempty"`
}

// Tool is the plan_write tool, bound to one session's plan store.
type Tool struct{ store Store }

// New returns the plan_write tool writing into store.
func New(store Store) tool.Tool { return Tool{store: store} }

// Name returns "plan_write".
func (Tool) Name() string { return Name }

// Description teaches the model the whole protocol: the three stages, what each one
// must contain, and that the run ends at a user approval gate it must not walk past.
// It lives here rather than in the system prompt because the tool is the thing being
// described, and because a desktop-only tool must not add weight to prompts the CLI
// also assembles.
func (Tool) Description() string {
	return `Record one stage of your plan-mode planning. Plan mode runs a fixed pipeline and this tool is how you advance it:

1. stage="understanding" — restate the goal, the boundaries, the acceptance criteria, and what is explicitly out of scope (goal, nonGoals). Ask nothing yet; record what you understood.
2. stage="design" — the ordered steps to carry the task out (title, steps). Each step must be concrete enough to execute: what changes, and where (files).
3. stage="review" — re-read your own design as a reviewer would and try to break it. Record what you found (reviewNotes), the risks and costs (risks), anything the user must decide (questions), and pass the REVISED final list in steps.

Call the stages in that order, one call each, within the same turn — after a successful call, immediately continue with the next stage; do not stop to ask the user in between. Calling out of order is rejected.

After the review call, the plan goes to the user: they edit, reorder, add or drop steps, and approve or cancel. STOP there — do not start implementing, and do not claim the work is done. Nothing is executed until the user approves, and their approval arrives as a new message.`
}

// InputSchema declares stage plus the per-stage payload fields. Everything except
// stage is optional at the schema level; which fields a given stage actually
// requires is enforced in Run, where the message can name the stage.
func (Tool) InputSchema() tool.Schema {
	stringList := func(desc string) tool.Schema {
		return tool.Schema{Type: tool.SchemaTypeArray, Description: desc, Items: &tool.Schema{Type: tool.SchemaTypeString}}
	}
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"stage": {
				Type:        tool.SchemaTypeString,
				Description: `Which stage this call records: "understanding", "design", or "review", in that order.`,
			},
			"goal":     {Type: tool.SchemaTypeString, Description: "understanding: the restated goal, boundaries and acceptance criteria."},
			"nonGoals": stringList("understanding: what is explicitly out of scope."),
			"title":    {Type: tool.SchemaTypeString, Description: "design: a short title for the approach."},
			"steps": {
				Type:        tool.SchemaTypeArray,
				Description: "design/review: the ordered steps to execute. On review, pass the complete revised list.",
				Items: &tool.Schema{
					Type: tool.SchemaTypeObject,
					Properties: map[string]tool.Schema{
						"title":  {Type: tool.SchemaTypeString, Description: "Imperative one-line description of the step."},
						"detail": {Type: tool.SchemaTypeString, Description: "How to carry it out; the specifics an executor needs."},
						"files":  stringList("Workspace-relative paths this step touches."),
					},
					Required:             []string{"title"},
					AdditionalProperties: false,
				},
			},
			"risks":       stringList("review: risks, costs and things that could go wrong."),
			"questions":   stringList("review: open questions only the user can settle."),
			"reviewNotes": stringList("review: what re-reading the design turned up — gaps, better options, rejected alternatives."),
		},
		Required:             []string{"stage"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe is false: each call advances a single shared pipeline, so two
// running at once would race over which stage the run is on.
func (Tool) IsConcurrencySafe() bool { return false }

// Run validates the call against its stage, hands it to the store, and returns the
// store's next-stage instruction. Validation failures and gate rejections both come
// back as errors, which the executor turns into an is_error result the model reads
// and corrects — no event is emitted for a rejected call, so the UI never shows a
// stage that did not happen.
func (t Tool) Run(_ context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse plan input: %w", err)
	}
	if t.store == nil {
		return tool.Result{}, errors.New("plan recording is unavailable in this session")
	}
	stage := strings.TrimSpace(in.Stage)
	doc, err := docFor(stage, in)
	if err != nil {
		return tool.Result{}, err
	}
	next, err := t.store.RecordStage(stage, doc)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: next}}}, nil
}

// docFor validates one stage's payload and shapes it into the document fragment that
// stage owns. Each stage checks only its own fields: a design call that also carries
// review notes is not an error worth failing a turn over, it just records what the
// stage is for.
func docFor(stage string, in input) (protocol.PlanDoc, error) {
	switch stage {
	case protocol.PlanStageUnderstanding:
		goal := strings.TrimSpace(in.Goal)
		if goal == "" {
			return protocol.PlanDoc{}, errors.New(`stage "understanding" requires goal: restate what is being asked, its boundaries and acceptance criteria`)
		}
		if len(goal) > maxTextChars {
			return protocol.PlanDoc{}, fmt.Errorf("goal is too long (%d > %d characters)", len(goal), maxTextChars)
		}
		return protocol.PlanDoc{Goal: goal, NonGoals: cleanList(in.NonGoals)}, nil

	case protocol.PlanStageDesign, protocol.PlanStageReview:
		steps, err := cleanSteps(in.Steps)
		if err != nil {
			return protocol.PlanDoc{}, fmt.Errorf("stage %q: %w", stage, err)
		}
		doc := protocol.PlanDoc{
			Title:       trimTo(in.Title, maxTitleChars),
			Steps:       steps,
			Risks:       cleanList(in.Risks),
			Questions:   cleanList(in.Questions),
			ReviewNotes: cleanList(in.ReviewNotes),
		}
		if stage == protocol.PlanStageReview && len(doc.ReviewNotes) == 0 && len(doc.Risks) == 0 {
			return protocol.PlanDoc{}, errors.New(`stage "review" requires reviewNotes or risks: say what re-reading the design actually turned up (write "无" only if you genuinely found nothing)`)
		}
		return doc, nil

	case "":
		return protocol.PlanDoc{}, errors.New(`stage is required: one of "understanding", "design", "review"`)
	default:
		return protocol.PlanDoc{}, fmt.Errorf(`unknown stage %q (want "understanding", "design" or "review")`, stage)
	}
}

// cleanSteps validates the ordered step list: at least one step, every step titled,
// bounded in count and size. IDs are the shell's to assign, so they are absent here.
func cleanSteps(steps []inputStep) ([]protocol.PlanStep, error) {
	if len(steps) == 0 {
		return nil, errors.New("steps is required (the ordered list to execute)")
	}
	if len(steps) > maxSteps {
		return nil, fmt.Errorf("too many steps (%d > %d) — group them into fewer, larger steps", len(steps), maxSteps)
	}
	out := make([]protocol.PlanStep, 0, len(steps))
	for i, s := range steps {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			return nil, fmt.Errorf("step %d: title is required", i+1)
		}
		out = append(out, protocol.PlanStep{
			Title:  trimTo(title, maxTitleChars),
			Detail: trimTo(s.Detail, maxTextChars),
			Files:  cleanList(s.Files),
		})
	}
	return out, nil
}

// cleanList drops blank entries, trims each one, and caps the list; nil for empty so
// the field stays out of the JSON.
func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := strings.TrimSpace(item); s != "" {
			out = append(out, trimTo(s, maxTextChars))
		}
		if len(out) == maxNotes {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// trimTo trims surrounding space and caps length by runes (never splitting a
// multi-byte character, which a byte slice would).
func trimTo(s string, limit int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
