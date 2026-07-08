# Desktop Conversation Redesign (Build-Log) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the desktop conversation + preview UI into a quiet "build-log": a full-width top title bar, file artifacts as one type-rail card each (VS Code-style SVG icons, no emoji), collapsed execution steps, flat user messages, auto-preview on turn end, and preview type-rail continuity.

**Architecture:** Add a per-file-kind accent color + SVG icon system (`kindAccent`/`kindIcon` in `preview.ts`, icons in `icons.tsx`). Lift window controls + the XRUN wordmark into a new full-width `TitleBar`. Render Write/Edit outputs as restyled type-rail `ArtifactCard`s (de-duplicated from the execution step list), carry the accent into the preview tab/header, and auto-open the turn's newest previewable artifact when the turn ends.

**Tech Stack:** React + TypeScript, Wails v2 runtime (window controls), existing `Icon`/`Markdown` components, vitest.

## Global Constraints

- vite (esbuild, no tsc gate); tests are vitest; no new npm dependencies; reuse existing `Icon`/`Markdown` (no new icon/markdown library). No emoji anywhere in the UI — all icons are stroke SVG.
- Reuse the app's existing theme tokens (`bg-surface`, `text-ink`, `text-muted`, `text-faint`, `border-line2`, `bg-inset`, `primary`); do NOT restyle unrelated pages (settings/skills/permissions/etc.).
- Type-accent colors (verbatim): html `#E39A3B`, markdown `#4C82F7`, code `#2FAE6A`, image/svg `#E0679B`, text/other `#8A94A6`.
- HTML preview iframe keeps `sandbox="allow-scripts allow-forms allow-popups allow-modals"` (no `allow-same-origin`) — do not touch it.
- Auto-preview: on turn end, open the turn's **newest** previewable Write/Edit file as a tab; gated by a `preview.autoOpen` localStorage toggle (default on).
- Preview pane width already guarded by `clampPreviewWidth` + `maxWidth:60%` — do not regress it.
- End every commit message with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

### Task 1: File-type SVG icons

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/icons.tsx`

**Interfaces:**
- Produces: `Icon` names `file-html`, `file-md`, `file-code`, `file-image`, `file-text` (stroke SVGs, `currentColor`).

- [ ] **Step 1: Add the five file-type icon cases**

In `icons.tsx`, inside the `switch (name)`, add (matching the existing `<svg {...common} {...stroke}>` pattern):

```tsx
    case 'file-html':
      return (
        <svg {...common} {...stroke}>
          <path d="M8.5 8 5 12l3.5 4" />
          <path d="M15.5 8 19 12l-3.5 4" />
        </svg>
      )
    case 'file-md':
      return (
        <svg {...common} {...stroke}>
          <rect x="2.5" y="6" width="19" height="12" rx="2.2" />
          <path d="M6 15V9.5l2.6 2.8L11.2 9.5V15" />
          <path d="M15.4 9.5v4.2m0 0-1.7-1.8m1.7 1.8 1.7-1.8" />
        </svg>
      )
    case 'file-code':
      return (
        <svg {...common} {...stroke}>
          <path d="M8 6c-2 0-3 1-3 3v1c0 1-.6 2-2 2 1.4 0 2 1 2 2v1c0 2 1 3 3 3" />
          <path d="M16 6c2 0 3 1 3 3v1c0 1 .6 2 2 2-1.4 0-2 1-2 2v1c0 2-1 3-3 3" />
        </svg>
      )
    case 'file-image':
      return (
        <svg {...common} {...stroke}>
          <rect x="3.5" y="4.5" width="17" height="15" rx="2.2" />
          <circle cx="9" cy="10" r="1.6" />
          <path d="M5 17l4.5-4.5 3.5 3.5 3-3 3 3" />
        </svg>
      )
    case 'file-text':
      return (
        <svg {...common} {...stroke}>
          <path d="M6 4h8l4 4v12a0 0 0 0 1 0 0H6a0 0 0 0 1 0 0V4z" />
          <path d="M14 4v4h4M8.5 13h7M8.5 16.5h7" />
        </svg>
      )
```

- [ ] **Step 2: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`.

- [ ] **Step 3: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/icons.tsx
git commit -m "Desktop UI: add file-type SVG icons (html/md/code/image/text)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: kindAccent + kindIcon mapping (preview.ts, pure)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview.ts`
- Modify: `cmd/runcode-desktop/frontend/src/preview.test.ts` (update kindIcon expectations; add kindAccent tests)

**Interfaces:**
- Consumes: `PreviewKind` (in `preview.ts`); `Icon` names from Task 1.
- Produces: `kindAccent(kind: PreviewKind): string` (accent hex); `kindIcon(kind: PreviewKind): string` now returns the Task-1 file-type icon names.

- [ ] **Step 1: Update the failing tests**

In `preview.test.ts`, REPLACE the existing `describe('kindIcon', ...)` block with:

```ts
describe('kindIcon', () => {
  it('maps kinds to file-type icon names', () => {
    expect(kindIcon('html')).toBe('file-html')
    expect(kindIcon('markdown')).toBe('file-md')
    expect(kindIcon('code')).toBe('file-code')
    expect(kindIcon('image')).toBe('file-image')
    expect(kindIcon('text')).toBe('file-text')
    expect(kindIcon('unsupported')).toBe('file-text')
  })
})

describe('kindAccent', () => {
  it('maps kinds to accent colors, slate for unknown', () => {
    expect(kindAccent('html')).toBe('#E39A3B')
    expect(kindAccent('markdown')).toBe('#4C82F7')
    expect(kindAccent('code')).toBe('#2FAE6A')
    expect(kindAccent('image')).toBe('#E0679B')
    expect(kindAccent('svg')).toBe('#E0679B')
    expect(kindAccent('unsupported')).toBe('#8A94A6')
  })
})
```

Add `kindAccent` to the top-of-file import from `./preview`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: FAIL — `kindAccent` not exported; `kindIcon('html')` returns the old `'globe'`.

- [ ] **Step 3: Update kindIcon and add kindAccent**

In `preview.ts`, REPLACE the existing `kindIcon` function with:

```ts
// kindIcon maps a preview kind to a file-type Icon name (see icons.tsx).
export function kindIcon(kind: PreviewKind): string {
  switch (kind) {
    case 'html': return 'file-html'
    case 'markdown': return 'file-md'
    case 'code': return 'file-code'
    case 'image': case 'svg': return 'file-image'
    default: return 'file-text'
  }
}

// kindAccent maps a preview kind to its accent color — the signature color-coding
// carried on artifact-card rails, the preview tab edge, and the type badge.
export function kindAccent(kind: PreviewKind): string {
  switch (kind) {
    case 'html': return '#E39A3B'
    case 'markdown': return '#4C82F7'
    case 'code': return '#2FAE6A'
    case 'image': case 'svg': return '#E0679B'
    default: return '#8A94A6'
  }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: PASS.

- [ ] **Step 5: Verify the build still compiles (kindIcon callers get new names)**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`. (`ArtifactCard` and `PreviewPanel` call `kindIcon`; the new names resolve to the Task-1 icons.)

- [ ] **Step 6: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.ts cmd/runcode-desktop/frontend/src/preview.test.ts
git commit -m "Desktop UI: kindIcon returns file-type icons; add kindAccent color map" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Full-width TitleBar + chrome restructure (App.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: existing `WindowControls`, `Logo`, `DRAG`/`NO_DRAG`, `Icon`.
- Produces: `TitleBar` component rendered at the top of both root branches.

- [ ] **Step 1: Add the TitleBar component**

Add near `WindowControls` (App.tsx ~line 1572):

```tsx
// TitleBar is the full-width top row (the frameless-window drag region): the XRUN
// wordmark on the left, an empty drag middle, and the window controls at the right.
function TitleBar() {
  return (
    <div className="h-[38px] flex-none flex items-center pl-3.5 bg-surface border-b border-line2 select-none" style={DRAG}>
      <span className="flex items-center gap-2 font-semibold text-[13.5px] tracking-tight">
        <span className="w-[20px] h-[20px] inline-flex items-center justify-center"><Logo size={18} /></span>
        XRUN
      </span>
      <div className="flex-1" />
      <WindowControls />
    </div>
  )
}
```

- [ ] **Step 2: Render TitleBar in the started (main) branch and drop the header's window controls**

In the main return (App.tsx ~line 983), insert `<TitleBar />` as the first child of the outer `<div className="flex flex-col h-screen">`, before `<div className="flex flex-1 min-h-0">`:

```tsx
    <div className="flex flex-col h-screen">
      <TitleBar />
      <div className="flex flex-1 min-h-0">
```

Then in the chat `<header>` (~line 1005), DELETE the `<WindowControls />` line (~1027). The header's right cluster keeps the ContextMeter + the "预览" button only. (The header keeps `style={DRAG}` — that's fine, it stays draggable.)

- [ ] **Step 3: Render TitleBar in the !started (start-form) branch**

In the `if (!started)` return (~line 957), REPLACE the thin drag bar + its WindowControls (~lines 959-962) so the same TitleBar is used:

```tsx
  if (!started) {
    return (
      <div className="flex flex-col h-screen">
        <TitleBar />
        {initialReq ? (
          <StartForm onStart={handleStart} starting={starting} error={startError} initial={initialReq} />
        ) : (
          <div className="flex-1" />
        )}
      </div>
    )
  }
```

- [ ] **Step 4: Remove the XRUN wordmark from the Sidebar**

In `Sidebar` (~line 1620), DELETE the logo block so the sidebar starts with the "新建对话" button:

```tsx
    <aside className="w-[268px] flex-none bg-surface border-r border-line2 flex flex-col p-4">
      <button className="w-full border-none bg-primary text-white font-semibold text-sm py-3 rounded-[11px] cursor-pointer inline-flex items-center justify-center gap-2 shadow-[0_5px_14px_rgba(91,108,240,0.3)] hover:brightness-105 transition" onClick={onNew}>
```

(i.e. remove the `<div className="flex items-center gap-2.5 px-1.5 pt-1 pb-[18px]"> … XRUN … </div>` block that preceded the button.)

- [ ] **Step 5: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green.

- [ ] **Step 6: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: lift window controls + XRUN into a full-width title bar" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Type-rail ArtifactCard restyle (artifact-card.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/artifact-card.tsx`

**Interfaces:**
- Consumes: `classifyPreview`, `artifactKindLabel`, `kindIcon`, `kindAccent` (`./preview`); `Icon` (`./icons`); existing `OpenWithMenu`.
- Produces: `ArtifactCard({ relPath, add, del, onOpen, autoOpened? })` — keeps the same props plus optional `autoOpened?: boolean` for the "已预览" badge.

- [ ] **Step 1: Restyle ArtifactCard as a type-rail card**

Update the imports to add `kindAccent`:

```tsx
import { classifyPreview, artifactKindLabel, kindIcon, kindAccent } from './preview'
```

REPLACE the `ArtifactCard` function with the type-rail version (left accent rail via `borderLeft`, mono filename, inline diff, hover "打开 →", optional auto-preview badge):

```tsx
// ArtifactCard renders one generated/edited file as a clickable type-rail card:
// a kind-colored left rail, a type icon, a monospace filename, the diff, and the
// open-with menu. Clicking opens an in-panel preview when the kind is previewable.
export function ArtifactCard({ relPath, add, del, onOpen, autoOpened }: { relPath: string; add: number; del: number; onOpen: (relPath: string) => void; autoOpened?: boolean }) {
  const { kind } = classifyPreview(relPath)
  const previewable = kind !== 'unsupported'
  const accent = kindAccent(kind)
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  return (
    <div
      onClick={() => previewable && onOpen(relPath)}
      style={{ borderLeftColor: accent, borderLeftWidth: 3 }}
      className={`group flex items-center gap-2.5 border border-line2 rounded-lg pl-3 pr-2.5 py-2 bg-surface ${previewable ? 'cursor-pointer hover:bg-surface2/40' : ''}`}
    >
      <span className="flex-none" style={{ color: accent }}><Icon name={kindIcon(kind)} size={17} /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 text-[13px] font-medium text-ink font-mono truncate" title={relPath}>
          {name}
          {autoOpened && <span className="flex-none text-[10px] font-sans font-semibold uppercase tracking-wide rounded px-1.5 py-0.5" style={{ color: accent, background: accent + '1a' }}>已预览</span>}
        </div>
        <div className="text-[11px] text-faint font-mono">
          {artifactKindLabel(kind)}
          {add + del > 0 && <span className="ml-1.5"><span className="text-green">+{add}</span> <span className={del > 0 ? 'text-red' : 'text-faint'}>−{del}</span></span>}
        </div>
      </div>
      <span className="flex-none text-[11px] text-faint font-mono opacity-0 group-hover:opacity-100 transition-opacity">打开 →</span>
      <OpenWithMenu relPath={relPath} previewable={previewable} onPreview={() => onOpen(relPath)} />
    </div>
  )
}
```

(Keep `OpenWithMenu` and `copyArtifactPath` unchanged. `accent + '1a'` is the accent at ~10% alpha for the badge background.)

- [ ] **Step 2: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`.

- [ ] **Step 3: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/artifact-card.tsx
git commit -m "Desktop UI: restyle ArtifactCard as a type-rail card" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Conversation — de-duplicate files, add eyebrow, flatten user message (App.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: `ArtifactCard` (Task 4), existing `previewableArtifacts`, `ExecutionCard`, `ToolEvent`.
- Produces: a de-duplicated turn render (file cards + non-file steps only) and a flat user-message style.

- [ ] **Step 1: Render non-file steps only in ExecutionCard, files as cards**

Find the exec-group render site (App.tsx ~line 1086, where `previewableArtifacts(g.tools)` and `<ArtifactCard>` render under `<ExecutionCard tools={g.tools} .../>`). Change it so the ExecutionCard shows only NON-file / non-previewable steps, and the previewable files render as cards with an eyebrow. Replace the ExecutionCard+cards block with:

```tsx
                <BotRow key={g.id}>
                  {(() => {
                    const arts = previewableArtifacts(g.tools)
                    const artPaths = new Set(arts.map((a) => a.path))
                    const steps = g.tools.filter((t) => {
                      const p = toolTargetPath(t)
                      return !(p && artPaths.has(p)) // hide tools already shown as artifact cards
                    })
                    return (
                      <>
                        {steps.length > 0 && <ExecutionCard tools={steps} harmAllows={harmAllows} />}
                        {arts.length > 0 && (
                          <div className="flex flex-col gap-1.5 mt-1.5">
                            <div className="text-[10.5px] font-semibold uppercase tracking-[0.14em] text-faint pl-0.5">写入</div>
                            {arts.map((a) => {
                              const rel = toWorkspaceRel(a.path, info?.cwd ?? '')
                              return (
                                <ArtifactCard
                                  key={a.path}
                                  relPath={rel}
                                  add={a.add}
                                  del={a.del}
                                  onOpen={openArtifact}
                                  autoOpened={tabs.some((t) => t.relPath === rel)}
                                />
                              )
                            })}
                          </div>
                        )}
                      </>
                    )
                  })()}
                </BotRow>
```

(This removes the duplication: a Write/Edit shown as an artifact card is filtered out of the ExecutionCard step list.)

- [ ] **Step 2: Flatten the user message style**

Find where a user block renders in the chat stream (search `b.kind === 'user'` in the blocks render, and the current user pill — a rounded `bg-inset`/inset chip with an ✕). Replace its container with a flat, right-aligned lavender block (no ✕ affordance):

```tsx
            <div className="flex justify-end my-1">
              <div className="max-w-[82%] rounded-[13px] px-3.5 py-2 text-[13.5px] leading-[1.55] text-ink" style={{ background: '#F4F3FF' }}>
                {b.text}
              </div>
            </div>
```

(Match the actual variable holding the user text — likely `b.text`. Remove the ✕/delete affordance that was on the old pill.)

- [ ] **Step 3: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green.

- [ ] **Step 4: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: de-duplicate file artifacts, add action eyebrow, flatten user message" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Preview type-rail continuity + header de-dup (preview-panel.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview-panel.tsx`

**Interfaces:**
- Consumes: `classifyPreview`, `kindAccent`, `artifactKindLabel`, `kindIcon` (`./preview`).
- Produces: tab active-edge + header badge colored by `kindAccent`; the filename removed from the `PreviewPanel` header (it stays in the tab).

- [ ] **Step 1: Color the active tab edge by kind**

In `preview-panel.tsx`, add `kindAccent` to the `./preview` import. In `PreviewTabs`, give the active tab a kind-colored left edge. Replace the tab `<div>` with one that sets the accent when active:

```tsx
        return (
          <div
            key={t.relPath}
            onClick={() => onSelect(t.relPath)}
            title={t.relPath}
            style={on ? { boxShadow: `inset 2px 0 0 ${kindAccent(classifyPreview(t.relPath).kind)}` } : undefined}
            className={`group flex items-center gap-1.5 pl-3 pr-2 max-w-[180px] flex-none cursor-pointer border-r border-line2 text-[12.5px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60'}`}
          >
```

- [ ] **Step 2: Color the header badge by kind and drop the filename**

In `PreviewPanel`'s header, compute the accent and remove the filename span (the name lives in the tab now); keep the kind icon (colored), the type badge (colored), and the toolbar. Replace the header `<div className="flex-none flex items-center ...">` contents:

```tsx
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <Icon name={kindIcon(kind)} size={15} className="flex-none" style={{ color: kindAccent(kind) }} />
        <span className="flex-none text-[10.5px] font-medium rounded px-1.5 py-0.5 mr-auto" style={{ color: kindAccent(kind), background: kindAccent(kind) + '1a' }}>{artifactKindLabel(kind)}</span>
        <IconBtn name="refresh" title="刷新" onClick={() => setBust((v) => v + 1)} />
        <IconBtn name="external-link" title="用系统默认程序打开" onClick={() => openExternal(relPath).catch(() => {})} />
        <IconBtn name="copy" title="复制路径" onClick={copyPath} />
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
```

(If `Icon` does not accept a `style` prop, wrap it: `<span style={{ color: kindAccent(kind) }}><Icon name={kindIcon(kind)} size={15} /></span>`. Check the `Icon` signature and use whichever compiles.)

- [ ] **Step 3: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green.

- [ ] **Step 4: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview-panel.tsx
git commit -m "Desktop UI: carry the type accent into preview tab + header, drop duplicate filename" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: Auto-preview on turn end + toggle (App.tsx + preview-panel.tsx)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`
- Modify: `cmd/runcode-desktop/frontend/src/preview-panel.tsx` (FileBrowser toggle)

**Interfaces:**
- Consumes: `previewableArtifacts`, `openArtifact`, `toWorkspaceRel`, `busy`, `blocks`.
- Produces: an `autoOpen` state persisted to `localStorage['preview.autoOpen']`; a turn-end effect that opens the newest previewable artifact; a toggle rendered in `FileBrowser`.

- [ ] **Step 1: Add the autoOpen state**

In App.tsx near the other preview state (by `previewWidth`, ~line 336), add:

```tsx
  const [autoOpen, setAutoOpen] = useState<boolean>(() => localStorage.getItem('preview.autoOpen') !== '0')
  const toggleAutoOpen = () => setAutoOpen((v) => { localStorage.setItem('preview.autoOpen', v ? '0' : '1'); return !v })
```

- [ ] **Step 2: Add the turn-end auto-open effect**

Add this effect in the component body (after `openArtifact` is defined). It fires on the busy true→false transition and opens the newest previewable artifact of the just-finished turn. It reads `blocks`/`autoOpen`/`info` via refs to avoid stale closures:

```tsx
  const autoRef = useRef({ autoOpen, blocks, cwd: info?.cwd ?? '' })
  autoRef.current = { autoOpen, blocks, cwd: info?.cwd ?? '' }
  const prevBusy = useRef(false)
  useEffect(() => {
    if (prevBusy.current && !busy) {
      const { autoOpen: on, blocks: bs, cwd } = autoRef.current
      if (on) {
        const tools = bs.filter((b) => b.kind === 'tool').map((b) => (b as Extract<Block, { kind: 'tool' }>).tool)
        const newest = previewableArtifacts(tools).at(-1)
        if (newest) openArtifact(toWorkspaceRel(newest.path, cwd))
      }
    }
    prevBusy.current = busy
  }, [busy])
```

(If the `Block` tool-variant extraction type differs, match the existing pattern used elsewhere — e.g. the `lastUser` extraction at ~line 656. `openArtifact` opens/focuses a tab and shows the pane.)

- [ ] **Step 3: Add the toggle to the FileBrowser**

In `preview-panel.tsx`, extend `FileBrowser` to accept `autoOpen`/`onToggleAutoOpen` and render a small toggle in its header:

```tsx
export function FileBrowser({ files, onPick, autoOpen, onToggleAutoOpen }: { files: string[]; onPick: (relPath: string) => void; autoOpen?: boolean; onToggleAutoOpen?: () => void }) {
```

In the FileBrowser header row (the `<div className="flex-none p-2 border-b border-line2">` that holds the filter input), add above/next to the input:

```tsx
      {onToggleAutoOpen && (
        <label className="flex items-center gap-1.5 mb-2 text-[12px] text-muted cursor-pointer select-none">
          <input type="checkbox" checked={!!autoOpen} onChange={onToggleAutoOpen} />
          写完自动预览
        </label>
      )}
```

Then where App.tsx renders `<FileBrowser files={files} onPick={...} />` (the browser branch of the aside), pass the toggle props: `<FileBrowser files={files} onPick={...} autoOpen={autoOpen} onToggleAutoOpen={toggleAutoOpen} />`.

- [ ] **Step 4: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green.

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx cmd/runcode-desktop/frontend/src/preview-panel.tsx
git commit -m "Desktop UI: auto-open the turn's newest previewable artifact, with a toggle" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: Package & manual verification

**Files:** none (build + manual).

- [ ] **Step 1: Full gate**

Run: `go build ./... && (cd cmd/runcode-desktop/frontend && npm run build && npx vitest run) && go -C cmd/runcode-desktop build ./...`
Expected: all green.

- [ ] **Step 2: Package build**

Run: `cd cmd/runcode-desktop && wails build`
Expected: `cmd/runcode-desktop/build/bin/XRUN.exe` produced.

- [ ] **Step 3: Manual verification**

Launch `build/bin/XRUN.exe`, open a workspace, and verify:
- The window controls (minimize / maximize / close) sit in the **top full-width title bar**, with the XRUN wordmark on its left; the sidebar no longer shows XRUN; the conversation status row (空闲 / context / 预览) has no window buttons.
- Ask the agent to write `demo.md`, `demo.html`, `demo.py`. Each renders as **one** type-rail card (blue / amber / green rail + matching SVG icon + mono filename + diff) — no duplicated step row for the same file. Bash/other steps collapse into the compact execution card.
- The user's messages are flat right-aligned lavender blocks (no ✕ pill).
- On turn end, the **newest** previewable file auto-opens as a tab and the pane shows; its tab edge + header badge match the file's accent color. Toggle "写完自动预览" off (in the file browser) → no auto-open; manual click still works.
- Drag the divider — the conversation never collapses (width guardrail holds).

- [ ] **Step 4: Commit (only if a config file changed; otherwise report results)**

---

## Notes for the implementer

- **Reuse:** `Icon`, `Markdown`, `OpenWithMenu`, `previewableArtifacts`, `toolTargetPath`, `diffStats`, `openArtifact`, `openTab` all already exist — do not redefine them.
- **kindIcon change (Task 2) is breaking for its callers by design:** `ArtifactCard` and `PreviewPanel` already call `kindIcon`; after Task 2 they render the new file-type icons (added in Task 1). Task 1 MUST land before Task 2 so the names resolve.
- **Accent alpha:** `accent + '1a'` appends ~10% hex alpha to a `#RRGGBB` accent for soft badge backgrounds. Only use it on 6-digit hex accents (all `kindAccent` values are).
- **Auto-open timing (Task 7):** the effect keys on `busy` only and reads other state via a ref, so it fires exactly once per turn on the true→false edge without stale closures. `previewableArtifacts(tools).at(-1)` is the turn's newest previewable file (dedup keeps first occurrence but order follows the tool stream; the last entry is the most recently written previewable file).
- **Do not touch** the iframe sandbox, the width clamp/`maxWidth:60%`, or unrelated pages.
