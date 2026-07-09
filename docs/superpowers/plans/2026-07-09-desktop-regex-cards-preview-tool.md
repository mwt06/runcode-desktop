# Regex Artifact Cards + `open_preview` Tool — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface artifact cards by regex-matching real workspace file paths in the assistant's reply (instead of from Write/Edit tool events), and add an `open_preview` agent tool the model calls to open a document/website in the desktop preview panel — registered in the desktop only, not the CLI.

**Architecture:** Two pure frontend helpers (`extractFilePaths`, `matchWorkspaceFiles`) drive cards rendered under each assistant reply. A Go `open_preview` tool validates a workspace path and emits a `tool.Event{Data:{path}}` (the TodoWrite pattern); the desktop injects it via a new `engine.Options.ExtraTools` (CLI leaves it empty), and the frontend opens a preview tab when it sees that tool event.

**Tech Stack:** Go (`pkg/tool`, `internal/toolpath`), React + TypeScript, vitest.

## Global Constraints

- Cross-platform (Mac/Win/Linux); the Go tool is pure stdlib and platform-agnostic. No new Go/npm dependencies. Go 1.26; vite (esbuild, no tsc gate).
- Cards come from **regex on the assistant reply text only** (not tool events, not tool output); only paths that **exist in the workspace file list** render.
- `open_preview` is registered **only in desktop mode** (`engine.Options.ExtraTools`); the CLI must never expose it. Main session only (not sub-agents), consistent with Task/Remember.
- The tool is **workspace-bounded** (reject out-of-workspace/nonexistent before emitting), reusing `internal/toolpath`.
- End every Go commit message with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

### Task 1: extractFilePaths + matchWorkspaceFiles (frontend, pure)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview.ts`
- Modify: `cmd/runcode-desktop/frontend/src/preview.test.ts`

**Interfaces:**
- Produces:
  - `extractFilePaths(text: string): string[]` — path-like tokens (word/path chars + short extension), trailing punctuation trimmed, deduped in order.
  - `matchWorkspaceFiles(candidates: string[], files: string[]): string[]` — the workspace-relative paths (forward-slash) that actually exist in `files`, matched by full path or `"/"+candidate` suffix, deduped in order.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/runcode-desktop/frontend/src/preview.test.ts` (add `extractFilePaths, matchWorkspaceFiles` to the `./preview` import):

```ts
describe('extractFilePaths', () => {
  it('pulls path-like tokens with a known-ish extension', () => {
    expect(extractFilePaths('我建了 cat.html 和 src/app.py 两个文件')).toEqual(['cat.html', 'src/app.py'])
  })
  it('trims trailing punctuation and leading ./', () => {
    expect(extractFilePaths('详见 ./README.md。')).toEqual(['README.md'])
    expect(extractFilePaths('见 report.md, notes.txt.')).toEqual(['report.md', 'notes.txt'])
  })
  it('ignores prose without a file extension', () => {
    expect(extractFilePaths('这是一段没有文件的普通文字')).toEqual([])
  })
})

describe('matchWorkspaceFiles', () => {
  const files = ['README.md', 'src/app.py', 'src/ui/index.html']
  it('keeps candidates that exist (full path or basename/suffix), workspace-relative', () => {
    expect(matchWorkspaceFiles(['app.py', 'README.md'], files)).toEqual(['src/app.py', 'README.md'])
    expect(matchWorkspaceFiles(['src/ui/index.html'], files)).toEqual(['src/ui/index.html'])
  })
  it('drops candidates that do not exist, dedups', () => {
    expect(matchWorkspaceFiles(['nope.md', 'README.md', 'README.md'], files)).toEqual(['README.md'])
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: FAIL — `extractFilePaths`/`matchWorkspaceFiles` not exported.

- [ ] **Step 3: Implement the two helpers**

Append to `cmd/runcode-desktop/frontend/src/preview.ts`:

```ts
// extractFilePaths pulls file-path-like tokens out of prose: word/path chars ending
// in a short extension. Permissive by design — matchWorkspaceFiles is the real gate
// (the path must exist in the workspace). Trailing ASCII/Chinese punctuation is
// trimmed; a leading "./" is dropped by the first-char class.
export function extractFilePaths(text: string): string[] {
  const re = /[A-Za-z0-9_@][\w./\\@+-]*\.[A-Za-z0-9]{1,12}/g
  const seen = new Set<string>()
  const out: string[] = []
  for (const m of text.matchAll(re)) {
    const tok = m[0].replace(/[)\]}.,;:!?，。、）】]+$/, '')
    if (tok && !seen.has(tok)) {
      seen.add(tok)
      out.push(tok)
    }
  }
  return out
}

// matchWorkspaceFiles keeps candidates that correspond to a real workspace file,
// returning the actual workspace-relative paths (forward-slash), deduped, in order.
// A candidate matches if — normalized — it equals a file path or a file path ends
// with "/" + the candidate (basename/suffix match).
export function matchWorkspaceFiles(candidates: string[], files: string[]): string[] {
  const norm = (s: string) => s.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
  const fileset = files.map(norm)
  const seen = new Set<string>()
  const out: string[] = []
  for (const c of candidates) {
    const cn = norm(c)
    if (!cn) continue
    const hit = fileset.find((f) => f === cn || f.endsWith('/' + cn))
    if (hit && !seen.has(hit)) {
      seen.add(hit)
      out.push(hit)
    }
  }
  return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.ts cmd/runcode-desktop/frontend/src/preview.test.ts
git commit -m "Desktop UI: add extractFilePaths + matchWorkspaceFiles for regex artifact cards" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: `open_preview` Go tool (backend, TDD)

**Files:**
- Create: `tools/preview/preview.go`
- Create: `tools/preview/preview_test.go`

**Interfaces:**
- Consumes: `pkg/tool` (Tool/Schema/Result/Event), `internal/toolpath` (`WorkspaceRoot`, `Resolve`, `IsWithinResolved`).
- Produces: `preview.New() tool.Tool` — Name `"open_preview"`, input `{path string}`, emits `tool.Event{ToolName:"open_preview", Data: previewData{Path}}` on success and returns a text Result; rejects out-of-workspace / missing files.

- [ ] **Step 1: Write the failing test**

Create `tools/preview/preview_test.go`:

```go
package preview

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wt68/runcode/pkg/tool"
)

func run(t *testing.T, ws, path string) (tool.Result, error, []tool.Event) {
	t.Helper()
	out := make(chan tool.Event, 4)
	tctx := &tool.Context{WorkingDirectory: ws}
	raw, _ := json.Marshal(map[string]string{"path": path})
	res, err := New().Run(context.Background(), raw, tctx, out)
	close(out)
	var evs []tool.Event
	for e := range out {
		evs = append(evs, e)
	}
	return res, err, evs
}

func TestOpenPreviewEmitsForWorkspaceFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "site.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err, evs := run(t, ws, "site.html")
	if err != nil || res.IsError {
		t.Fatalf("expected success, got err=%v res=%+v", err, res)
	}
	if len(evs) != 1 || evs[0].ToolName != "open_preview" {
		t.Fatalf("expected one open_preview event, got %+v", evs)
	}
	pd, ok := evs[0].Data.(previewData)
	if !ok || pd.Path != "site.html" {
		t.Fatalf("event Data = %#v, want previewData{Path: site.html}", evs[0].Data)
	}
}

func TestOpenPreviewRejectsEscapeAndMissing(t *testing.T) {
	ws := t.TempDir()
	if _, err, evs := run(t, ws, "../../secret.txt"); err == nil || len(evs) != 0 {
		t.Fatal("escape should error with no event")
	}
	if _, err, evs := run(t, ws, "nope.md"); err == nil || len(evs) != 0 {
		t.Fatal("missing file should error with no event")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./tools/preview/ -v`
Expected: FAIL — package `preview` does not exist.

- [ ] **Step 3: Implement the tool**

Create `tools/preview/preview.go`:

```go
// Package preview implements the open_preview tool: the model asks the desktop to
// open a workspace file in its preview panel. It validates the path is inside the
// workspace and exists, then emits a structured event the desktop UI acts on. It is
// registered only in the desktop (via engine.Options.ExtraTools), never the CLI.
package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wt68/runcode/internal/toolpath"
	"github.com/wt68/runcode/pkg/tool"
)

// previewData is the structured payload the desktop UI reads off the event to open
// a preview tab. In-process UI only.
type previewData struct {
	Path string `json:"path"`
}

type input struct {
	Path string `json:"path"`
}

// Tool is the open_preview tool.
type Tool struct{}

// New returns the open_preview tool.
func New() tool.Tool { return Tool{} }

func (Tool) Name() string { return "open_preview" }

func (Tool) Description() string {
	return "Open a workspace file in the user's desktop preview panel. Call this after you " +
		"produce a document or a website/H5 (e.g. an .html, .md, or image) so the user sees it " +
		"immediately. The path is a workspace-relative file path."
}

func (Tool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString, Description: "Workspace-relative path of the file to preview."},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
}

func (Tool) IsConcurrencySafe() bool { return true }

func (Tool) Run(_ context.Context, raw json.RawMessage, tctx *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse open_preview input: %w", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.Result{}, errors.New("path is required")
	}
	ws, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return tool.Result{}, err
	}
	abs, err := toolpath.Resolve(in.Path, tctx)
	if err != nil {
		return tool.Result{}, err
	}
	within, err := toolpath.IsWithinResolved(ws, abs)
	if err != nil || !within {
		return tool.Result{}, fmt.Errorf("path is outside the workspace: %s", in.Path)
	}
	if info, err := os.Stat(abs); err != nil || info.IsDir() {
		return tool.Result{}, fmt.Errorf("file not found: %s", in.Path)
	}
	rel, err := filepath.Rel(ws, abs)
	if err != nil {
		rel = in.Path
	}
	rel = filepath.ToSlash(rel)
	if out != nil {
		select {
		case out <- tool.Event{Type: tool.EventTypeProgress, ToolName: "open_preview", Message: "预览 " + rel, Data: previewData{Path: rel}}:
		default:
		}
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: "已在桌面打开预览：" + rel}}}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w tools/preview/ && go test ./tools/preview/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
go build ./...
git add tools/preview/preview.go tools/preview/preview_test.go
git commit -m "Add open_preview tool: signal the desktop to preview a workspace file" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Wire `ExtraTools` into the engine + register in the desktop

**Files:**
- Modify: `internal/engine/engine.go` (`Options.ExtraTools`)
- Modify: `internal/engine/build.go` (append `opts.ExtraTools`)
- Modify: `internal/desktop/app.go` (pass `ExtraTools: []tool.Tool{preview.New()}`)

**Interfaces:**
- Consumes: `preview.New()` (Task 2).
- Produces: `engine.Options.ExtraTools []tool.Tool` — extra tools appended to the main session's tool set (not sub-agents); empty in the CLI.

- [ ] **Step 1: Add the Options field**

In `internal/engine/engine.go`, add to the `Options` struct (after `TelemetryWriter`, ~line 53):

```go
	// ExtraTools are host-supplied tools appended to the main session's tool set
	// (after the sub-agent snapshot, so sub-agents don't get them). The desktop uses
	// this to register the open_preview tool; the CLI leaves it nil.
	ExtraTools []tool.Tool
```

(`tool` is already imported in `engine.go`.)

- [ ] **Step 2: Append ExtraTools in build.go**

In `internal/engine/build.go`, after the memory tool is appended (`sessionTools = append(sessionTools, memory.NewTool(memStore))`, ~line 190) and before `session, err := repl.NewSession(...)`, add:

```go
	// Host-supplied extra tools (e.g. the desktop's open_preview), main session only.
	sessionTools = append(sessionTools, opts.ExtraTools...)
```

- [ ] **Step 3: Register the tool in the desktop**

In `internal/desktop/app.go`, at the `engine.Build(cfg, engine.Options{...})` call (~line 127), add `ExtraTools` to the Options literal:

```go
		ExtraTools: []tool.Tool{preview.New()},
```

Add the imports to `app.go`: `"github.com/wt68/runcode/pkg/tool"` and `"github.com/wt68/runcode/tools/preview"` (check for existing `tool`/`preview` imports first to avoid duplicates).

- [ ] **Step 4: Verify build (core + desktop module)**

Run: `gofmt -w internal/ && go build ./... && go -C cmd/runcode-desktop build ./...`
Expected: exit 0 for both. (The CLI `cmd/runcode` doesn't set `ExtraTools`, so `open_preview` is absent there.)

- [ ] **Step 5: Commit**

```bash
git add internal/engine/engine.go internal/engine/build.go internal/desktop/app.go
git commit -m "Engine: add Options.ExtraTools; register open_preview in the desktop only" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Frontend wiring — regex cards, remove tool-event cards, open_preview open, files refresh (App.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: `extractFilePaths`, `matchWorkspaceFiles` (Task 1); `ArtifactCard`; `openArtifact`, `toWorkspaceRel`, `listFiles`, `setFiles`, `files`, `tabs`, `info`.

- [ ] **Step 1: Import the regex helpers**

In `App.tsx`, add to the existing `./preview` import: `extractFilePaths, matchWorkspaceFiles`.

- [ ] **Step 2: Revert the exec group to plain steps (remove tool-event cards + de-dup)**

Find the `g.kind === 'exec'` render (App.tsx ~1081-1105, currently a `previewableArtifacts`/`steps`-filtered block with `<ArtifactCard>`s). REPLACE that whole `<BotRow key={g.id}>…</BotRow>` block with the plain form (execution card shows all the group's tools; no artifact cards here):

```tsx
                <BotRow key={g.id}><ExecutionCard tools={g.tools} harmAllows={harmAllows} /></BotRow>
```

Then grep `App.tsx` for `previewableArtifacts` — if it is now unused (the auto-open effect uses `toolTargetPath`, not `previewableArtifacts`), delete the `previewableArtifacts` helper function to avoid dead code. If a reference remains, leave it.

- [ ] **Step 3: Render regex cards under each assistant block**

Find the `g.kind === 'block'` render (App.tsx ~1147, `<BlockView key={g.block.id} block={g.block} />`). Wrap it in a NEUTRAL `<div>` (no styling — it must not change `BlockView`'s own per-kind layout, e.g. the right-aligned user message) and append regex cards only for an assistant block:

```tsx
                <div key={g.block.id}>
                  <BlockView block={g.block} />
                  {g.block.kind === 'assistant' && (() => {
                    const paths = matchWorkspaceFiles(extractFilePaths(g.block.text), files)
                    return paths.length > 0 ? (
                      <div className="flex flex-col gap-1.5 mt-1.5">
                        {paths.map((p) => (
                          <ArtifactCard key={p} relPath={p} add={0} del={0} onOpen={openArtifact} autoOpened={tabs.some((t) => t.relPath === p)} />
                        ))}
                      </div>
                    ) : null
                  })()}
                </div>
```

(The `key` moves from `<BlockView>` to the wrapping `<div>`. `BlockView` renders its full row inside; the cards render below it. Only `kind === 'assistant'` gets cards — user/error blocks are untouched.)

- [ ] **Step 4: Open a preview when the open_preview tool fires**

Add a ref that always holds the latest preview-opener, near the other refs (e.g. by `autoRef`):

```tsx
  const openPreviewRef = useRef<(path: string) => void>(() => {})
  openPreviewRef.current = (path: string) => openArtifact(toWorkspaceRel(path, info?.cwd ?? ''))
```

In the `onEvent<ToolEvent>(Events.ToolEvent, (ev) => { ... })` handler (App.tsx ~607), right after the `TodoWrite` intercept block (the `if (!ev.parentToolUseID && ev.toolName === 'TodoWrite') { ... return }`), add — this triggers the preview but does NOT return, so the tool still renders as a normal step:

```tsx
        if (!ev.parentToolUseID && ev.toolName === 'open_preview') {
          const p = (ev.data as { path?: string } | undefined)?.path
          if (p) openPreviewRef.current(p)
        }
```

- [ ] **Step 5: Refresh the file list on turn end (so new files match)**

In the `onEvent<TurnEnd>(Events.TurnEnd, (end) => { ... })` handler (App.tsx ~653), after `setBusy(false)`, add a file-list refresh so files written this turn become matchable by the regex cards:

```tsx
        listFiles().then((f) => setFiles(f ?? [])).catch(() => {})
```

(Match the existing `listFiles().then((f) => setFiles(f ?? []))` shape already used elsewhere in the file.)

- [ ] **Step 6: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green. Grep to confirm the exec group no longer renders `<ArtifactCard>`, and the assistant block does.

- [ ] **Step 7: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: regex artifact cards under replies; open_preview opens a tab; refresh files on turn end" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Package & manual verification

**Files:** none (build + manual).

- [ ] **Step 1: Full gate**

Run: `go build ./... && go test ./tools/preview/... ./internal/desktop/... && (cd cmd/runcode-desktop/frontend && npm run build && npx vitest run) && go -C cmd/runcode-desktop build ./...`
Expected: all green.

- [ ] **Step 2: Package build**

Run: `cd cmd/runcode-desktop && wails build`
Expected: `cmd/runcode-desktop/build/bin/XRUN.exe` produced.

- [ ] **Step 3: Manual verification**

Launch the app, open a workspace, and verify:
- Ask the agent to create `site.html`; if it mentions `site.html` in its reply, an **artifact card appears under the reply** (regex-matched), clickable to preview. A file the agent writes but never names in prose gets **no** card (expected — regex-only).
- Ask the agent to "create a webpage and open a preview": it calls `open_preview("site.html")` → the file **auto-opens as a preview tab**; the `open_preview` call also shows as a normal step in the execution card.
- A card is shown only for files that **exist** in the workspace (a made-up `index.html` mentioned in prose but not on disk → no card).
- (CLI) Run the CLI build (`runcode`) and confirm the model's tool list does **not** include `open_preview`.

- [ ] **Step 4: Commit (only if a config file changed; otherwise report results)**

---

## Notes for the implementer

- **Reuse:** `ArtifactCard`, `openArtifact`, `toWorkspaceRel`, `listFiles`/`setFiles`/`files`, `tabs`, `Events.ToolEvent`/`TurnEnd` handlers all already exist — do not redefine.
- **Regex cards use `add={0} del={0}`** — `ArtifactCard` only shows the diff when `add+del>0`, so text-derived cards show name + type + open-with, no diff. Correct.
- **open_preview fall-through:** the ToolEvent intercept for `open_preview` must NOT `return` (unlike TodoWrite) — the call should still render as a step in the execution card.
- **Stale-closure safety:** the ToolEvent handler is registered in an effect; read the opener via `openPreviewRef.current` (reassigned every render) so it uses fresh `openArtifact`/`info.cwd`.
- **Do not touch** the auto-open-on-turn-end effect, the width guardrail, the preview panel, or the CLI's tool registration.
