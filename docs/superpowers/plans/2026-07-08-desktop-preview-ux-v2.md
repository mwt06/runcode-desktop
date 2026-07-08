# Desktop Preview UX v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Upgrade the desktop artifact-preview UX to match the reference: conversation file **cards**, a **multi-tab** preview pane, an **open-with** menu, a **drag-resizable** pane, a restyled panel, and a file-browser filter.

**Architecture:** Extract one shared workspace-path containment helper on the Go side and add three OS bindings (open externally / reveal in folder / resolve absolute path) that reuse it. On the frontend, a pure tab reducer drives a multi-tab preview pane; conversation Write/Edit outputs render as clickable artifact cards that open tabs; the pane is drag-resizable with width persisted to localStorage.

**Tech Stack:** Go (`os/exec`, standard library), Wails v2 runtime (`ClipboardSetText`), React + TypeScript, existing `Markdown`/`Icon` components, vitest.

## Global Constraints

- Go 1.26; no new external Go dependencies (standard library only). `internal/desktop` stays Wails-free.
- All three new Go bindings resolve `relPath` through the shared `resolveWithinWorkspace` helper and **fail closed** on any workspace escape (`..`, symlink, Windows junction) — reject before launching any process.
- Frontend: vite (esbuild, no tsc gate); tests are vitest; no new npm dependencies; reuse the existing `Markdown` and `Icon` components (no new markdown/highlight/icon library).
- The "open-with" menu has exactly four items: `预览` / `用系统默认程序打开` / `在文件夹中显示` / `复制路径`. "复制路径" copies the **absolute** path.
- HTML preview iframe keeps `sandbox="allow-scripts allow-forms allow-popups allow-modals"` (no `allow-same-origin`).
- Preview pane width persists to `localStorage` key `preview.width`; clamp to `[360, floor(window.innerWidth * 0.6)]`.
- End every Go commit message and PR body with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

### Task 1: Shared workspace-path containment helper (backend refactor)

**Files:**
- Create: `internal/desktop/workspacepath.go`
- Create: `internal/desktop/workspacepath_test.go`
- Modify: `internal/desktop/preview.go` (`ReadArtifact` uses the helper)
- Modify: `internal/desktop/preview_server.go` (`previewPathWithinRoot` uses the helper)

**Interfaces:**
- Produces: `resolveWithinWorkspace(ws, relPath string) (resolved string, err error)` — returns the symlink-resolved absolute path of a workspace file, or an error if `ws` is empty, the path escapes lexically, or it resolves (symlink/junction) outside `ws`. Fails closed: a non-existent path or a reparse point Go cannot walk returns an error.

- [ ] **Step 1: Write the failing test**

Create `internal/desktop/workspacepath_test.go`:

```go
package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveWithinWorkspaceReturnsResolved(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWithinWorkspace(ws, "a.txt")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// EvalSymlinks may canonicalize (e.g. macOS /var→/private/var); compare by suffix.
	if !strings.HasSuffix(got, "a.txt") {
		t.Fatalf("resolved = %q, want .../a.txt", got)
	}
}

func TestResolveWithinWorkspaceRejects(t *testing.T) {
	ws := t.TempDir()
	if _, err := resolveWithinWorkspace("", "a.txt"); err == nil {
		t.Fatal("empty ws should error")
	}
	if _, err := resolveWithinWorkspace(ws, "../../secret.txt"); err == nil {
		t.Fatal("lexical escape should error")
	}
	if _, err := resolveWithinWorkspace(ws, "nope.txt"); err == nil {
		t.Fatal("non-existent path should fail closed (error)")
	}
}

func TestResolveWithinWorkspaceRejectsJunctionEscape(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are Windows-only")
	}
	ws := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(ws, "evil"), outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, out)
	}
	if _, err := resolveWithinWorkspace(ws, "evil/secret.txt"); err == nil {
		t.Fatal("junction escape should error (fail closed)")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/desktop/ -run 'TestResolveWithinWorkspace' -v`
Expected: FAIL — `undefined: resolveWithinWorkspace`.

- [ ] **Step 3: Write the helper**

Create `internal/desktop/workspacepath.go`:

```go
package desktop

import (
	"errors"
	"path/filepath"
	"strings"
)

// resolveWithinWorkspace resolves relPath against workspace ws and returns the
// real (symlink-resolved) absolute path, or an error if ws is empty, relPath
// escapes lexically, or it resolves (via symlink/junction) outside ws. It fails
// closed: a non-existent path, or a reparse point Go cannot walk (Windows
// junctions are ModeIrregular, not ModeSymlink, and abort EvalSymlinks), returns
// an error rather than a path the OS might follow outside ws. This is the single
// containment check reused by ReadArtifact, the preview static server, and the
// open/reveal bindings.
func resolveWithinWorkspace(ws, relPath string) (string, error) {
	if ws == "" {
		return "", errors.New("no active workspace")
	}
	full := filepath.Join(ws, filepath.FromSlash(relPath))
	// Lexical bound first (cheap; catches ".." before touching the filesystem).
	if rel, err := filepath.Rel(ws, full); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the workspace")
	}
	wsResolved, err := filepath.EvalSymlinks(ws)
	if err != nil {
		wsResolved = ws
	}
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	if r, err := filepath.Rel(wsResolved, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("path resolves outside the workspace")
	}
	return resolved, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/desktop/ -run 'TestResolveWithinWorkspace' -v`
Expected: PASS (the junction test runs on Windows; the others everywhere).

- [ ] **Step 5: Refactor `ReadArtifact` to use the helper**

In `internal/desktop/preview.go`, replace the body of `ReadArtifact` (the lexical + resolved bound + EvalSymlinks block, currently lines ~20-45) so it delegates containment to the helper. The final function body:

```go
func (a *App) ReadArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	resolved, err := resolveWithinWorkspace(ws, relPath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.Size() > maxArtifactBytes {
		return "", errors.New("file too large to preview")
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("file is not text")
	}
	return string(data), nil
}
```

Remove the now-unused imports from `preview.go` if any (`strings` and `filepath` are no longer used by `ReadArtifact`; check whether other functions in the file still need them — `startPreview`/`stopPreview` do not, so drop `strings` and `filepath` from the import block if nothing else uses them). Keep `errors`, `os`, `unicode/utf8`.

- [ ] **Step 6: Refactor `previewPathWithinRoot` to use the helper**

In `internal/desktop/preview_server.go`, replace `previewPathWithinRoot` (currently lines ~60-84) with:

```go
// previewPathWithinRoot reports whether the request path stays inside root,
// reusing the shared fail-closed containment check (so the static server and
// ReadArtifact enforce identical boundaries).
func previewPathWithinRoot(root, urlPath string) bool {
	_, err := resolveWithinWorkspace(root, strings.TrimPrefix(urlPath, "/"))
	return err == nil
}
```

If, after this, `preview_server.go` no longer uses `filepath` directly, drop it from that file's import block (keep `net`, `net/http`, `strings`, `time`). Verify by building.

- [ ] **Step 7: Run the full desktop suite to confirm no regression**

Run: `gofmt -w internal/desktop/ && go build ./... && go test ./internal/desktop/ -run 'Preview|ReadArtifact|ResolveWithin' -v`
Expected: PASS — the existing `TestPreviewServer*`, `TestReadArtifact*` (including `..hidden` and junction escape), and the new `TestResolveWithinWorkspace*` all green. Build exit 0.

- [ ] **Step 8: Commit**

```bash
git add internal/desktop/workspacepath.go internal/desktop/workspacepath_test.go internal/desktop/preview.go internal/desktop/preview_server.go
git commit -m "Desktop: extract shared workspace-path containment helper" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Open-with backend bindings

**Files:**
- Create: `internal/desktop/open.go`
- Create: `internal/desktop/open_test.go`

**Interfaces:**
- Consumes: `resolveWithinWorkspace` (Task 1).
- Produces: `(*App).OpenExternal(relPath string) error`, `(*App).RevealInFolder(relPath string) error`, `(*App).ResolveArtifactPath(relPath string) (string, error)` — each rejects out-of-workspace paths before doing anything OS-side.

- [ ] **Step 1: Write the failing tests**

Create `internal/desktop/open_test.go`:

```go
package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveArtifactPathInWorkspace(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})
	a.workspace = ws
	got, err := a.ResolveArtifactPath("a.md")
	if err != nil || !strings.HasSuffix(got, "a.md") {
		t.Fatalf("ResolveArtifactPath = (%q, %v), want .../a.md, nil", got, err)
	}
}

func TestOpenBindingsRejectEscape(t *testing.T) {
	ws := t.TempDir()
	a := New(&recordingSink{})
	a.workspace = ws
	// Escaping paths must error BEFORE any OS launch — no process is started.
	if err := a.OpenExternal("../../evil.txt"); err == nil {
		t.Fatal("OpenExternal allowed an out-of-workspace path")
	}
	if err := a.RevealInFolder("../../evil.txt"); err == nil {
		t.Fatal("RevealInFolder allowed an out-of-workspace path")
	}
	if _, err := a.ResolveArtifactPath("../../evil.txt"); err == nil {
		t.Fatal("ResolveArtifactPath allowed an out-of-workspace path")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/desktop/ -run 'TestResolveArtifactPath|TestOpenBindings' -v`
Expected: FAIL — `a.ResolveArtifactPath undefined`.

- [ ] **Step 3: Write the bindings**

Create `internal/desktop/open.go`:

```go
package desktop

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// OpenExternal opens the workspace file with the OS default application. It is
// bounded to the workspace (rejects escapes before launching).
func (a *App) OpenExternal(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return err
	}
	return openInOS(full)
}

// RevealInFolder shows the workspace file in the OS file manager.
func (a *App) RevealInFolder(relPath string) error {
	full, err := a.resolveArtifact(relPath)
	if err != nil {
		return err
	}
	return revealInOS(full)
}

// ResolveArtifactPath returns the absolute path of a workspace file, for the
// UI's "copy path" action.
func (a *App) ResolveArtifactPath(relPath string) (string, error) {
	return a.resolveArtifact(relPath)
}

func (a *App) resolveArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	return resolveWithinWorkspace(ws, relPath)
}

func openInOS(path string) error {
	switch runtime.GOOS {
	case "windows":
		// start needs an (empty) title arg first so a quoted path isn't taken as one.
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}

func revealInOS(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+path).Start()
	case "darwin":
		return exec.Command("open", "-R", path).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/desktop/ -run 'TestResolveArtifactPath|TestOpenBindings' -v`
Expected: PASS. (No process is launched — the escape tests error first, and `ResolveArtifactPath` never launches.)

- [ ] **Step 5: Build + commit**

```bash
gofmt -w internal/desktop/ && go build ./...
git add internal/desktop/open.go internal/desktop/open_test.go
git commit -m "Desktop: add OpenExternal/RevealInFolder/ResolveArtifactPath bindings" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Preview tab reducer (frontend, pure)

**Files:**
- Create: `cmd/runcode-desktop/frontend/src/preview-tabs.ts`
- Create: `cmd/runcode-desktop/frontend/src/preview-tabs.test.ts`

**Interfaces:**
- Produces:
  - `type PreviewTab = { relPath: string }`
  - `openTab(tabs, active, relPath) => { tabs: PreviewTab[]; active: string }`
  - `closeTab(tabs, active, relPath) => { tabs: PreviewTab[]; active: string | null }`
  - `setActive(tabs, active, relPath) => string | null`

- [ ] **Step 1: Write the failing tests**

Create `cmd/runcode-desktop/frontend/src/preview-tabs.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { openTab, closeTab, setActive, type PreviewTab } from './preview-tabs'

const tabs = (...p: string[]): PreviewTab[] => p.map((relPath) => ({ relPath }))

describe('openTab', () => {
  it('appends a new tab and focuses it', () => {
    expect(openTab(tabs('a'), 'a', 'b')).toEqual({ tabs: tabs('a', 'b'), active: 'b' })
  })
  it('focuses an existing tab without duplicating', () => {
    expect(openTab(tabs('a', 'b'), 'a', 'b')).toEqual({ tabs: tabs('a', 'b'), active: 'b' })
  })
})

describe('closeTab', () => {
  it('closing the active tab focuses the right neighbor', () => {
    expect(closeTab(tabs('a', 'b', 'c'), 'b', 'b')).toEqual({ tabs: tabs('a', 'c'), active: 'c' })
  })
  it('closing the active last tab focuses the left neighbor', () => {
    expect(closeTab(tabs('a', 'b'), 'b', 'b')).toEqual({ tabs: tabs('a'), active: 'a' })
  })
  it('closing the only tab yields null active', () => {
    expect(closeTab(tabs('a'), 'a', 'a')).toEqual({ tabs: [], active: null })
  })
  it('closing a non-active tab keeps active', () => {
    expect(closeTab(tabs('a', 'b'), 'a', 'b')).toEqual({ tabs: tabs('a'), active: 'a' })
  })
})

describe('setActive', () => {
  it('activates an existing tab, ignores unknown', () => {
    expect(setActive(tabs('a', 'b'), 'a', 'b')).toBe('b')
    expect(setActive(tabs('a', 'b'), 'a', 'zzz')).toBe('a')
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview-tabs.test.ts`
Expected: FAIL — cannot resolve `./preview-tabs`.

- [ ] **Step 3: Write the reducer**

Create `cmd/runcode-desktop/frontend/src/preview-tabs.ts`:

```ts
export type PreviewTab = { relPath: string }

// openTab appends relPath as a new tab (focused), or just focuses it if already open.
export function openTab(tabs: PreviewTab[], _active: string | null, relPath: string): { tabs: PreviewTab[]; active: string } {
  if (tabs.some((t) => t.relPath === relPath)) return { tabs, active: relPath }
  return { tabs: [...tabs, { relPath }], active: relPath }
}

// closeTab removes relPath. If it was the active tab, focus moves to the right
// neighbor, else the left, else null (no tabs left).
export function closeTab(tabs: PreviewTab[], active: string | null, relPath: string): { tabs: PreviewTab[]; active: string | null } {
  const idx = tabs.findIndex((t) => t.relPath === relPath)
  if (idx === -1) return { tabs, active }
  const next = tabs.filter((t) => t.relPath !== relPath)
  if (active !== relPath) return { tabs: next, active }
  const neighbor = next[idx] ?? next[idx - 1] ?? null
  return { tabs: next, active: neighbor ? neighbor.relPath : null }
}

// setActive focuses relPath if it is an open tab, otherwise leaves active unchanged.
export function setActive(tabs: PreviewTab[], active: string | null, relPath: string): string | null {
  return tabs.some((t) => t.relPath === relPath) ? relPath : active
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview-tabs.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview-tabs.ts cmd/runcode-desktop/frontend/src/preview-tabs.test.ts
git commit -m "Desktop UI: add preview tab reducer" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Kind label, kind icon, file filter (frontend, pure)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview.ts` (append three functions)
- Modify: `cmd/runcode-desktop/frontend/src/preview.test.ts` (append tests)

**Interfaces:**
- Consumes: `PreviewKind` (already in `preview.ts`).
- Produces:
  - `artifactKindLabel(kind: PreviewKind): string` — Chinese type subtitle.
  - `kindIcon(kind: PreviewKind): string` — an `Icon` name for that kind.
  - `filterFiles(files: string[], query: string): string[]` — case-insensitive substring filter (empty query returns all).

- [ ] **Step 1: Write the failing tests**

Append to `cmd/runcode-desktop/frontend/src/preview.test.ts`:

```ts
import { artifactKindLabel, kindIcon, filterFiles } from './preview'

describe('artifactKindLabel', () => {
  it('maps kinds to Chinese subtitles', () => {
    expect(artifactKindLabel('markdown')).toBe('Markdown 文档')
    expect(artifactKindLabel('image')).toBe('图像')
    expect(artifactKindLabel('code')).toBe('代码')
    expect(artifactKindLabel('unsupported')).toBe('文件')
  })
})

describe('kindIcon', () => {
  it('maps kinds to existing Icon names', () => {
    expect(kindIcon('html')).toBe('globe')
    expect(kindIcon('code')).toBe('terminal')
    expect(kindIcon('markdown')).toBe('file')
  })
})

describe('filterFiles', () => {
  it('filters case-insensitively; empty query returns all', () => {
    const fs = ['src/App.tsx', 'README.md', 'src/preview.ts']
    expect(filterFiles(fs, '')).toEqual(fs)
    expect(filterFiles(fs, 'app')).toEqual(['src/App.tsx'])
    expect(filterFiles(fs, 'SRC/')).toEqual(['src/App.tsx', 'src/preview.ts'])
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: FAIL — `artifactKindLabel`/`kindIcon`/`filterFiles` not exported.

- [ ] **Step 3: Write the functions**

Append to `cmd/runcode-desktop/frontend/src/preview.ts`:

```ts
// artifactKindLabel is the Chinese type subtitle shown on artifact cards and the
// preview header.
export function artifactKindLabel(kind: PreviewKind): string {
  switch (kind) {
    case 'markdown': return 'Markdown 文档'
    case 'html': return 'HTML 页面'
    case 'image': return '图像'
    case 'svg': return 'SVG 矢量图'
    case 'code': return '代码'
    case 'text': return '文本'
    default: return '文件'
  }
}

// kindIcon maps a preview kind to an existing Icon name (see icons.tsx).
export function kindIcon(kind: PreviewKind): string {
  switch (kind) {
    case 'html': return 'globe'
    case 'code': return 'terminal'
    default: return 'file'
  }
}

// filterFiles keeps workspace-relative paths that contain query (case-insensitive);
// an empty/whitespace query returns the list unchanged.
export function filterFiles(files: string[], query: string): string[] {
  const q = query.trim().toLowerCase()
  if (!q) return files
  return files.filter((f) => f.toLowerCase().includes(q))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: PASS (all preview.ts tests, old + new).

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.ts cmd/runcode-desktop/frontend/src/preview.test.ts
git commit -m "Desktop UI: add artifact kind label/icon and file filter helpers" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Bridge bindings, Wails types, new icons (frontend plumbing)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/bridge.ts`
- Modify: `cmd/runcode-desktop/frontend/src/wails.d.ts`
- Modify: `cmd/runcode-desktop/frontend/src/icons.tsx`

**Interfaces:**
- Consumes: backend `OpenExternal`/`RevealInFolder`/`ResolveArtifactPath` (Task 2); Wails runtime `ClipboardSetText`.
- Produces: `openExternal(relPath)`, `revealInFolder(relPath)`, `resolveArtifactPath(relPath)`, `copyText(text)` (from `bridge.ts`); `Icon` names `refresh`, `external-link`, `copy`.

- [ ] **Step 1: Add the bridge bindings + copyText**

In `cmd/runcode-desktop/frontend/src/bridge.ts`, add near `readArtifact`:

```ts
export const openExternal = (relPath: string) => app().OpenExternal(relPath) as Promise<void>
export const revealInFolder = (relPath: string) => app().RevealInFolder(relPath) as Promise<void>
export const resolveArtifactPath = (relPath: string) => app().ResolveArtifactPath(relPath) as Promise<string>

// copyText writes to the clipboard via the Wails runtime, falling back to the
// browser clipboard API.
export async function copyText(text: string): Promise<void> {
  try {
    await window.runtime.ClipboardSetText(text)
  } catch {
    await navigator.clipboard.writeText(text)
  }
}
```

- [ ] **Step 2: Add the Wails types**

In `cmd/runcode-desktop/frontend/src/wails.d.ts`, add to the `App: { ... }` interface (near `ReadArtifact`):

```ts
          OpenExternal(relPath: string): Promise<void>
          RevealInFolder(relPath: string): Promise<void>
          ResolveArtifactPath(relPath: string): Promise<string>
```

and to the `runtime: { ... }` interface (near `BrowserOpenURL`):

```ts
      ClipboardSetText(text: string): Promise<boolean>
```

- [ ] **Step 3: Add the three icons**

In `cmd/runcode-desktop/frontend/src/icons.tsx`, add these cases inside the `switch (name)` (matching the existing `<svg {...common} {...stroke}>` pattern):

```tsx
    case 'refresh':
      return (
        <svg {...common} {...stroke}>
          <path d="M21 12a9 9 0 1 1-2.64-6.36" />
          <path d="M21 3v5h-5" />
        </svg>
      )
    case 'external-link':
      return (
        <svg {...common} {...stroke}>
          <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
          <path d="M15 3h6v6" />
          <path d="M10 14 21 3" />
        </svg>
      )
    case 'copy':
      return (
        <svg {...common} {...stroke}>
          <rect x="9" y="9" width="12" height="12" rx="2" />
          <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
        </svg>
      )
```

- [ ] **Step 4: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`. (Generated Wails bindings for the new App methods appear after the next `wails build`; `app()` calls compile now because bindings are loosely typed at call sites.)

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/bridge.ts cmd/runcode-desktop/frontend/src/wails.d.ts cmd/runcode-desktop/frontend/src/icons.tsx
git commit -m "Desktop UI: bridge open-with actions, clipboard, and toolbar icons" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: ArtifactCard + OpenWithMenu component

**Files:**
- Create: `cmd/runcode-desktop/frontend/src/artifact-card.tsx`

**Interfaces:**
- Consumes: `classifyPreview`, `artifactKindLabel`, `kindIcon` (`./preview`); `openExternal`, `revealInFolder`, `resolveArtifactPath`, `copyText` (`./bridge`); `Icon` (`./icons`).
- Produces: `ArtifactCard({ relPath, add, del, onOpen }: { relPath: string; add: number; del: number; onOpen: (relPath: string) => void })`.

- [ ] **Step 1: Write the component**

Create `cmd/runcode-desktop/frontend/src/artifact-card.tsx`:

```tsx
import { useState } from 'react'
import { Icon } from './icons'
import { classifyPreview, artifactKindLabel, kindIcon } from './preview'
import { openExternal, revealInFolder, resolveArtifactPath, copyText } from './bridge'

async function copyArtifactPath(relPath: string) {
  try {
    const abs = await resolveArtifactPath(relPath)
    await copyText(abs)
  } catch {
    /* best-effort: clipboard/resolve failure is non-fatal */
  }
}

// OpenWithMenu is the "打开方式" dropdown: preview (in-panel), open with the OS
// default app, reveal in the file manager, copy the absolute path.
function OpenWithMenu({ relPath, previewable, onPreview }: { relPath: string; previewable: boolean; onPreview: () => void }) {
  const [open, setOpen] = useState(false)
  const item = 'w-full text-left px-3 py-1.5 hover:bg-surface2 whitespace-nowrap'
  return (
    <div className="relative flex-none">
      <button
        onClick={(e) => { e.stopPropagation(); setOpen((v) => !v) }}
        className="flex items-center gap-1 text-[12px] text-muted hover:text-ink border border-line2 rounded-md px-2 py-1"
      >
        打开方式 <Icon name="chevron-down" size={12} />
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-10" onClick={(e) => { e.stopPropagation(); setOpen(false) }} />
          <div className="absolute right-0 mt-1 z-20 min-w-[168px] bg-surface border border-line2 rounded-lg shadow-card py-1 text-[12.5px] text-ink">
            <button className={`${item} ${previewable ? '' : 'text-faint cursor-default'}`} disabled={!previewable} onClick={(e) => { e.stopPropagation(); onPreview(); setOpen(false) }}>预览</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); openExternal(relPath); setOpen(false) }}>用系统默认程序打开</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); revealInFolder(relPath); setOpen(false) }}>在文件夹中显示</button>
            <button className={item} onClick={(e) => { e.stopPropagation(); copyArtifactPath(relPath); setOpen(false) }}>复制路径</button>
          </div>
        </>
      )}
    </div>
  )
}

// ArtifactCard renders one generated/edited file as a clickable card in the
// conversation: type icon, filename, type subtitle + diff, and the open-with menu.
// Clicking the card opens an in-panel preview (when the type is previewable).
export function ArtifactCard({ relPath, add, del, onOpen }: { relPath: string; add: number; del: number; onOpen: (relPath: string) => void }) {
  const { kind } = classifyPreview(relPath)
  const previewable = kind !== 'unsupported'
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  return (
    <div
      onClick={() => previewable && onOpen(relPath)}
      className={`flex items-center gap-2.5 border border-line2 rounded-xl px-3 py-2 bg-surface ${previewable ? 'cursor-pointer hover:border-primary/50 hover:bg-surface2/40' : ''}`}
    >
      <span className="flex-none w-8 h-8 rounded-lg bg-inset flex items-center justify-center text-muted"><Icon name={kindIcon(kind)} size={16} /></span>
      <div className="flex-1 min-w-0">
        <div className="text-[13px] font-medium text-ink truncate" title={relPath}>{name}</div>
        <div className="text-[11px] text-faint">
          {artifactKindLabel(kind)}
          {add + del > 0 && (
            <span className="ml-2 font-mono"><span className="text-green">+{add}</span> <span className={del > 0 ? 'text-red' : 'text-faint'}>−{del}</span></span>
          )}
        </div>
      </div>
      <OpenWithMenu relPath={relPath} previewable={previewable} onPreview={() => onOpen(relPath)} />
    </div>
  )
}
```

- [ ] **Step 2: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`.

- [ ] **Step 3: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/artifact-card.tsx
git commit -m "Desktop UI: add ArtifactCard with open-with menu" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: PreviewTabs + PreviewPane + restyle + FileBrowser filter

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview-panel.tsx`

**Interfaces:**
- Consumes: `PreviewTab` (`./preview-tabs`); `classifyPreview`, `previewSrc`, `artifactKindLabel`, `kindIcon`, `filterFiles`, `buildFileTree`, `FileNode` (`./preview`); `readArtifact`, `openExternal`, `resolveArtifactPath`, `copyText` (`./bridge`); `Icon` (`./icons`); `Markdown` (`./markdown`).
- Produces: `PreviewTabs({ tabs, active, onSelect, onClose })`; `PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab })`; the existing `FileBrowser` gains a `filter` search box. `PreviewPanel` keeps signature `({ baseURL, relPath, onClose })`.

- [ ] **Step 1: Restyle PreviewPanel's header and swap to real OS open**

In `preview-panel.tsx`, update the imports at the top:

```tsx
import { useEffect, useState, type ReactNode } from 'react'
import { Markdown } from './markdown'
import { readArtifact, openExternal, resolveArtifactPath, copyText } from './bridge'
import { Icon } from './icons'
import { classifyPreview, previewSrc, artifactKindLabel, kindIcon, filterFiles, buildFileTree, type FileNode } from './preview'
import type { PreviewTab } from './preview-tabs'
```

Replace the `PreviewPanel` header block (the `<div className="flex-none flex items-center gap-2 h-[52px] ...">` … `</div>`) with an icon toolbar, and change the two "用系统程序打开" handlers to call `openExternal(relPath)` (real OS open) instead of `window.runtime.BrowserOpenURL(...)`. Add a `copyPath` handler and an `IconBtn` helper. The full replacement `PreviewPanel` body:

```tsx
function IconBtn({ name, title, onClick }: { name: string; title: string; onClick: () => void }) {
  return (
    <button title={title} onClick={onClick} className="flex-none w-7 h-7 flex items-center justify-center rounded-md text-muted hover:text-ink hover:bg-surface2">
      <Icon name={name} size={14} />
    </button>
  )
}

export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    let ignore = false
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then((t) => { if (!ignore) setText(t) })
      .catch((e) => { if (!ignore) setErr(String(e)) })
    return () => { ignore = true }
  }, [relPath, kind, bust, textual])

  const copyPath = async () => {
    try { await copyText(await resolveArtifactPath(relPath)) } catch { /* non-fatal */ }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <Icon name={kindIcon(kind)} size={15} className="flex-none text-muted" />
        <span className="text-[13px] font-medium text-ink truncate flex-1 min-w-0" title={relPath}>{name}</span>
        <span className="flex-none text-[10.5px] text-faint bg-inset rounded px-1.5 py-0.5 mr-0.5">{artifactKindLabel(kind)}</span>
        <IconBtn name="refresh" title="刷新" onClick={() => setBust((v) => v + 1)} />
        <IconBtn name="external-link" title="用系统默认程序打开" onClick={() => openExternal(relPath)} />
        <IconBtn name="copy" title="复制路径" onClick={copyPath} />
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4 flex items-center justify-center min-h-full bg-inset/30"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {kind === 'code' && text != null && <div className="p-4"><Markdown>{fencedCode(text, lang)}</Markdown></div>}
        {kind === 'text' && text != null && (
          <pre className="m-0 p-4 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words">{text}</pre>
        )}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。<button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath)}>用系统程序打开</button></div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
      </div>
    </div>
  )
}
```

(Keep the existing `fencedCode` helper at the top of the file unchanged.)

- [ ] **Step 2: Add PreviewTabs and PreviewPane**

Add these components to `preview-panel.tsx` (after `PreviewPanel`):

```tsx
// PreviewTabs is the tab strip above the preview: one tab per open file, active
// highlighted, each closable.
export function PreviewTabs({ tabs, active, onSelect, onClose }: { tabs: PreviewTab[]; active: string | null; onSelect: (relPath: string) => void; onClose: (relPath: string) => void }) {
  return (
    <div className="flex-none flex items-stretch h-[36px] border-b border-line2 overflow-x-auto bg-surface">
      {tabs.map((t) => {
        const name = t.relPath.replace(/\\/g, '/').split('/').pop() || t.relPath
        const on = t.relPath === active
        return (
          <div
            key={t.relPath}
            onClick={() => onSelect(t.relPath)}
            title={t.relPath}
            className={`group flex items-center gap-1.5 pl-3 pr-2 max-w-[180px] flex-none cursor-pointer border-r border-line2 text-[12.5px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60'}`}
          >
            <span className="truncate">{name}</span>
            <button
              className="flex-none w-4 h-4 flex items-center justify-center rounded opacity-0 group-hover:opacity-100 hover:bg-line2 hover:text-ink"
              onClick={(e) => { e.stopPropagation(); onClose(t.relPath) }}
            >
              <Icon name="win-close" size={10} />
            </button>
          </div>
        )
      })}
    </div>
  )
}

// PreviewPane composes the tab strip with the active file's PreviewPanel.
export function PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab }: { tabs: PreviewTab[]; active: string | null; baseURL: string; onSelect: (relPath: string) => void; onClose: () => void; onCloseTab: (relPath: string) => void }) {
  return (
    <div className="flex flex-col h-full min-h-0">
      <PreviewTabs tabs={tabs} active={active} onSelect={onSelect} onClose={onCloseTab} />
      <div className="flex-1 min-h-0">
        {active ? <PreviewPanel key={active} baseURL={baseURL} relPath={active} onClose={onClose} /> : null}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Add the filter box to FileBrowser**

Replace the existing `FileBrowser` with a version that has a filter input:

```tsx
export function FileBrowser({ files, onPick }: { files: string[]; onPick: (relPath: string) => void }) {
  const [query, setQuery] = useState('')
  const tree = buildFileTree(filterFiles(files, query))
  const render = (nodes: FileNode[], depth: number): ReactNode =>
    nodes.map((n) =>
      n.dir ? (
        <div key={n.path}>
          <div className="px-2 py-1 text-[12.5px] text-muted font-medium" style={{ paddingLeft: 8 + depth * 12 }}>{n.name}/</div>
          {n.children && render(n.children, depth + 1)}
        </div>
      ) : (
        <div key={n.path} onClick={() => onPick(n.path)} className="px-2 py-1 text-[12.5px] text-ink hover:bg-surface2 cursor-pointer truncate" style={{ paddingLeft: 8 + depth * 12 }} title={n.path}>{n.name}</div>
      ),
    )
  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex-none p-2 border-b border-line2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="筛选文件…"
          className="w-full text-[12.5px] bg-inset rounded-md px-2.5 py-1.5 outline-none text-ink placeholder:text-faint"
        />
      </div>
      <div className="flex-1 min-h-0 text-[12.5px] py-1 overflow-auto">{render(tree, 0)}</div>
    </div>
  )
}
```

- [ ] **Step 4: Verify it builds + vitest still green**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview-panel.tsx
git commit -m "Desktop UI: tabbed preview pane, restyled panel, file filter" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: App.tsx integration — tabs, artifact cards, resizable pane

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: `openTab`, `closeTab`, `setActive`, `PreviewTab` (`./preview-tabs`); `PreviewPane`, `FileBrowser` (`./preview-panel`); `ArtifactCard` (`./artifact-card`); `isPreviewable`, `toWorkspaceRel` (`./preview`); existing `toolTargetPath`, `diffStats`, `listFiles`, `info`, `files`.

- [ ] **Step 1: Update imports and replace preview state**

At the top of `App.tsx`, change the preview imports to:

```tsx
import { PreviewPane, FileBrowser } from './preview-panel'
import { ArtifactCard } from './artifact-card'
import { isPreviewable, toWorkspaceRel } from './preview'
import { openTab, closeTab, setActive, type PreviewTab } from './preview-tabs'
```

**Delete** the `const [previewPath, setPreviewPath] = useState<string | null>(null)` line (v1) and replace it with the tab + width state below. **Do NOT** add a `browseOpen` line here — it already exists from v1; leave that existing declaration in place.

```tsx
  const [tabs, setTabs] = useState<PreviewTab[]>([])
  const [activeTab, setActiveTab] = useState<string | null>(null)
  const [previewWidth, setPreviewWidth] = useState<number>(() => {
    const v = Number(localStorage.getItem('preview.width'))
    return v >= 360 ? v : 560
  })
```

After all of Task 8's edits, grep `App.tsx` for `previewPath` and `setPreviewPath` — there must be **zero** remaining references (Steps 3–5 replace every v1 use); the build fails otherwise.

- [ ] **Step 2: Add the openArtifact + tab handlers + drag handlers**

Add these near the other handlers in the component body:

```tsx
  const openArtifact = (rel: string) => {
    const r = openTab(tabs, activeTab, rel)
    setTabs(r.tabs)
    setActiveTab(r.active)
    setBrowseOpen(false)
  }
  const closePreviewTab = (rel: string) => {
    const r = closeTab(tabs, activeTab, rel)
    setTabs(r.tabs)
    setActiveTab(r.active)
  }
  const dragW = useRef<{ startX: number; startW: number } | null>(null)
  const onPreviewDragStart = (e: React.PointerEvent) => {
    dragW.current = { startX: e.clientX, startW: previewWidth }
    ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
  }
  const onPreviewDragMove = (e: React.PointerEvent) => {
    if (!dragW.current) return
    const dx = dragW.current.startX - e.clientX // dragging the left edge leftward grows the pane
    const w = Math.min(Math.max(dragW.current.startW + dx, 360), Math.floor(window.innerWidth * 0.6))
    setPreviewWidth(w)
  }
  const onPreviewDragEnd = () => {
    if (dragW.current) {
      localStorage.setItem('preview.width', String(previewWidth))
      dragW.current = null
    }
  }
```

Ensure `useRef` is imported from `react` at the top (add it to the existing `import { ... } from 'react'`).

- [ ] **Step 3: Replace the right pane (`<aside>`) with the drag handle + tabbed pane**

Find the preview `<aside>` block (the one guarded by `view === 'chat' && (previewPath || browseOpen)`) and replace the whole block with:

```tsx
        {view === 'chat' && (tabs.length > 0 || browseOpen) && (
          <>
            <div
              className="w-[5px] flex-none cursor-col-resize hover:bg-primary/30 active:bg-primary/40"
              onPointerDown={onPreviewDragStart}
              onPointerMove={onPreviewDragMove}
              onPointerUp={onPreviewDragEnd}
            />
            <aside style={{ width: previewWidth }} className="flex-none border-l border-line2 flex flex-col min-h-0 bg-surface">
              {tabs.length > 0 ? (
                <PreviewPane
                  tabs={tabs}
                  active={activeTab}
                  baseURL={info?.previewBaseURL ?? ''}
                  onSelect={(p) => setActiveTab(setActive(tabs, activeTab, p))}
                  onCloseTab={closePreviewTab}
                  onClose={() => { setTabs([]); setActiveTab(null) }}
                />
              ) : (
                <div className="flex flex-col h-full min-h-0">
                  <div className="flex-none flex items-center h-[44px] px-3 border-b border-line2">
                    <span className="text-[13px] font-medium text-ink flex-1">文件预览</span>
                    <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={() => setBrowseOpen(false)}>✕</button>
                  </div>
                  <div className="flex-1 min-h-0">
                    <FileBrowser files={files} onPick={(p) => { if (isPreviewable(p)) openArtifact(toWorkspaceRel(p, info?.cwd ?? '')) }} />
                  </div>
                </div>
              )}
            </aside>
          </>
        )}
```

- [ ] **Step 4: Replace the tool-card preview button with artifact cards**

In `ExecutionCard`, remove the `onPreview` prop from its signature and delete the preview `<button>` block (the one gated on `onPreview && (t.toolName === 'Write' ...`). Keep the `toolTargetPath` helper (it is reused below). `ExecutionCard`'s signature returns to:

```tsx
function ExecutionCard({ tools, harmAllows }: { tools: ToolEvent[]; harmAllows?: Record<string, string> }) {
```

Add a helper near `toolTargetPath` that collects a group's previewable artifacts:

```tsx
// previewableArtifacts extracts the workspace files a group's Write/Edit steps
// produced, de-duplicated, for rendering as artifact cards.
function previewableArtifacts(tools: ToolEvent[]): { path: string; add: number; del: number }[] {
  const seen = new Set<string>()
  const out: { path: string; add: number; del: number }[] = []
  for (const t of tools) {
    if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
      const p = toolTargetPath(t)
      if (p && isPreviewable(p) && !seen.has(p)) {
        seen.add(p)
        const { add, del } = diffStats(t)
        out.push({ path: p, add, del })
      }
    }
  }
  return out
}
```

At the exec-group render site (currently `<BotRow key={g.id}><ExecutionCard tools={g.tools} harmAllows={harmAllows} onPreview={...} /></BotRow>` around line 1004), drop the `onPreview` prop and render artifact cards under the card:

```tsx
                <BotRow key={g.id}>
                  <ExecutionCard tools={g.tools} harmAllows={harmAllows} />
                  {(() => {
                    const arts = previewableArtifacts(g.tools)
                    return arts.length > 0 ? (
                      <div className="flex flex-col gap-1.5 mt-1.5">
                        {arts.map((a) => (
                          <ArtifactCard
                            key={a.path}
                            relPath={toWorkspaceRel(a.path, info?.cwd ?? '')}
                            add={a.add}
                            del={a.del}
                            onOpen={openArtifact}
                          />
                        ))}
                      </div>
                    ) : null
                  })()}
                </BotRow>
```

- [ ] **Step 5: Keep the header "预览" toggle working with the new state**

The header toggle button (added in v1) sets `browseOpen` and previously `setPreviewPath(null)`. Change that handler to clear tabs-based state is NOT wanted (opening the browser shouldn't close tabs); just toggle the browser. Update its onClick to:

```tsx
onClick={() => { setBrowseOpen((v) => !v); if (browseOpen) return; /* opening: refresh files */ listFiles().then((f) => setFiles(f ?? [])).catch(() => {}) }}
```

(Match the existing `listFiles().then(...)` shape already used in the file — if it differs, reuse that exact call. The browser only shows when `tabs.length === 0`, so opening it while tabs are present is a no-op visually; that is acceptable for v1.)

- [ ] **Step 6: Verify build + vitest**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest all pass. Fix any type/JSX error surfaced now that `artifact-card.tsx`, `preview-panel.tsx`, and `preview-tabs.ts` are all imported for the first time together.

- [ ] **Step 7: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: conversation artifact cards, multi-tab preview, resizable pane" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: Package & manual verification

**Files:** none (build + manual verification).

- [ ] **Step 1: Full Go + frontend gate**

Run: `go build ./... && go test ./internal/desktop/... && (cd cmd/runcode-desktop/frontend && npm run build && npx vitest run)`
Expected: Go build exit 0, desktop tests pass, `✓ built`, vitest green.

- [ ] **Step 2: Desktop Go module + package build**

Run: `go -C cmd/runcode-desktop build ./... && (cd cmd/runcode-desktop && wails build)`
Expected: exit 0; `cmd/runcode-desktop/build/bin/XRUN.exe` produced (Wails regenerates JS bindings for the three new App methods).

- [ ] **Step 3: Manual verification**

Launch `build/bin/XRUN.exe`, open a workspace, and verify:
- Ask the agent to write `demo.md`, `demo.html` (with `<img src="./logo.png">` + a `logo.png`), and `demo.py`. Each shows as an **artifact card** under the execution card (icon + name + type + diff + "打开方式").
- Click a card → opens in the right pane as a **tab**; open all three → three tabs, switch and close them; closing the active tab focuses a neighbor.
- HTML renders **with its image resolved** (static server); Markdown renders; Python is syntax-highlighted.
- "打开方式" menu: 预览 opens the tab; 用系统默认程序打开 launches the OS app; 在文件夹中显示 opens the file manager at the file; 复制路径 puts the absolute path on the clipboard.
- Drag the divider between conversation and pane — it resizes, clamps at the min, and the width persists across an app restart.
- Header "预览" opens the file browser (when no tabs); the filter box narrows the tree.

- [ ] **Step 4: Commit (if any build-config change was needed)**

Only if a file changed (e.g. CSP). Otherwise no commit — report the manual results.

---

## Notes for the implementer

- **Reuse over rebuild:** `Markdown` (`./markdown`), `Icon` (`./icons`), and the highlight styles already exist — do not add libraries.
- **`toolTargetPath` / `diffStats`** already exist in `App.tsx` (from the v1 preview work) — reuse them; do not redefine.
- **Wails bindings:** the three new Go methods are callable from JS after `wails build` regenerates bindings; `npm run build` (esbuild) compiles the frontend without them because `app()` calls aren't type-checked against generated bindings.
- **localStorage width:** the read happens once in the `useState` initializer; the write happens on drag end. `window.innerWidth` is read at drag time for the max clamp.
- **Open-with side effects:** `OpenExternal`/`RevealInFolder` launch OS processes; they are not unit-tested for the launch itself — only that out-of-workspace paths are rejected before launching.
