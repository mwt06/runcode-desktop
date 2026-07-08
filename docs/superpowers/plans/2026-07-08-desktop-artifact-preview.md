# Desktop Artifact Preview (Sub-project A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a right-split preview panel to the desktop app that renders AI-produced artifacts (Markdown / HTML(H5) / image / SVG / code / text), backed by a loopback read-only static file server so HTML resolves its assets.

**Architecture:** A Go `previewServer` serves the active workspace over `127.0.0.1:<random>` (read-only); the desktop App starts/stops it per session and exposes its base URL via `SessionInfo`. A `ReadArtifact` binding returns text content for React-rendered types. The frontend classifies a file by extension, renders HTML/images by static-server URL (HTML in a sandboxed iframe) and Markdown/code/text via `ReadArtifact`, and auto-marks previewable `Write`/`Edit` outputs plus a file-browser fallback.

**Tech Stack:** Go (`net/http`, standard library), Wails v2, React + TypeScript, existing `Markdown` component (react-markdown), vitest.

## Global Constraints

- Go 1.26; no new external Go dependencies (use the standard library and existing `internal/toolpath`).
- Backend lives in the main module `internal/desktop` (transport-agnostic; no Wails imports). Frontend in `cmd/runcode-desktop/frontend/src`.
- The static server binds **loopback only** (`127.0.0.1`), serves **read-only** (GET/HEAD), and must not serve outside the workspace (`..` and symlink escapes rejected).
- HTML previews render in a sandboxed `<iframe sandbox="allow-scripts allow-forms allow-popups allow-modals">` **without** `allow-same-origin`.
- End every Go commit message and PR body per repo convention (`Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`).
- Frontend build is `vite build` (esbuild, no tsc gate); tests are `vitest`.

---

### Task 1: Loopback static preview server (backend)

**Files:**
- Create: `internal/desktop/preview_server.go`
- Test: `internal/desktop/preview_server_test.go`

**Interfaces:**
- Produces: `type previewServer struct{}`; `newPreviewServer() *previewServer`; `(*previewServer).start(workspace string) (baseURL string, err error)`; `(*previewServer).stop()`. `baseURL` ends with `/` (e.g. `http://127.0.0.1:52713/`).

- [ ] **Step 1: Write the failing tests**

Create `internal/desktop/preview_server_test.go`:

```go
package desktop

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func getPreview(t *testing.T, baseURL, rel string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(baseURL + rel)
	if err != nil {
		t.Fatalf("GET %s: %v", rel, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(body)
}

func TestPreviewServerServesWorkspaceFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "index.html"), []byte("<h1>hi</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := newPreviewServer()
	base, err := ps.start(ws)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ps.stop()
	if !strings.HasPrefix(base, "http://127.0.0.1:") || !strings.HasSuffix(base, "/") {
		t.Fatalf("baseURL = %q, want http://127.0.0.1:<port>/", base)
	}
	resp, body := getPreview(t, base, "index.html")
	if resp.StatusCode != 200 || body != "<h1>hi</h1>" {
		t.Fatalf("serve = %d %q, want 200 <h1>hi</h1>", resp.StatusCode, body)
	}
}

func TestPreviewServerRejectsTraversal(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	// %2e%2e%2f = "../" ; must not escape the workspace.
	resp, _ := getPreview(t, base, "%2e%2e%2f%2e%2e%2fwindows%2fwin.ini")
	if resp.StatusCode == 200 {
		t.Fatal("traversal request was served (should be 403/404)")
	}
}

func TestPreviewServerRejectsNonGET(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	defer ps.stop()
	resp, err := http.Post(base+"index.html", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", resp.StatusCode)
	}
}

func TestPreviewServerStopCloses(t *testing.T) {
	ws := t.TempDir()
	ps := newPreviewServer()
	base, _ := ps.start(ws)
	ps.stop()
	if _, err := http.Get(base + "index.html"); err == nil {
		t.Fatal("server still reachable after stop()")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/desktop/ -run 'TestPreviewServer' -v`
Expected: FAIL — `undefined: newPreviewServer`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/desktop/preview_server.go`:

```go
package desktop

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
)

// previewServer serves the active workspace read-only over loopback HTTP, so the
// preview panel can load HTML (with its relative assets) and images by URL. It is
// bound to 127.0.0.1 only, so no other host can reach it.
type previewServer struct {
	srv *http.Server
	ln  net.Listener
}

func newPreviewServer() *previewServer { return &previewServer{} }

// start serves workspace on 127.0.0.1:<os-assigned-port> and returns the base URL
// (ending with "/"). It is read-only (GET/HEAD) and refuses to serve outside the
// workspace.
func (p *previewServer) start(workspace string) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	fs := http.FileServer(http.Dir(root))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !previewPathWithinRoot(root, r.URL.Path) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		fs.ServeHTTP(w, r)
	})
	p.ln = ln
	p.srv = &http.Server{Handler: handler}
	go func() { _ = p.srv.Serve(ln) }()
	return "http://" + ln.Addr().String() + "/", nil
}

func (p *previewServer) stop() {
	if p.srv != nil {
		_ = p.srv.Close()
		p.srv = nil
		p.ln = nil
	}
}

// previewPathWithinRoot reports whether the request path stays inside root. http.Dir
// already blocks lexical ".." traversal; this additionally rejects symlink escapes.
func previewPathWithinRoot(root, urlPath string) bool {
	clean := filepath.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	full := filepath.Join(root, strings.TrimPrefix(clean, "/"))
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		// Non-existent target: http.FileServer will 404; the lexical join above
		// already cannot escape root, so allow it through.
		return true
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		rootResolved = root
	}
	rel, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/desktop/ -run 'TestPreviewServer' -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/preview_server.go internal/desktop/preview_server_test.go
git commit -m "Desktop: add a loopback read-only preview static server

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: ReadArtifact binding (backend)

**Files:**
- Create: `internal/desktop/preview.go`
- Test: `internal/desktop/preview_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `(*App).ReadArtifact(relPath string) (string, error)` — resolves `relPath` against the active workspace, rejects escapes, caps at `maxArtifactBytes` (2 MiB), rejects non-UTF-8/binary. `const maxArtifactBytes = 2 << 20`.

- [ ] **Step 1: Write the failing tests**

Create `internal/desktop/preview_test.go`:

```go
package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appWithWorkspace(t *testing.T, ws string) *App {
	t.Helper()
	a := New(&recordingSink{})
	a.workspace = ws
	return a
}

func TestReadArtifactReturnsWorkspaceText(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "a.md"), []byte("# hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	got, err := a.ReadArtifact("a.md")
	if err != nil || got != "# hi" {
		t.Fatalf("ReadArtifact = (%q, %v), want (# hi, nil)", got, err)
	}
}

func TestReadArtifactRejectsOutsideWorkspace(t *testing.T) {
	ws := t.TempDir()
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("../../secret.txt"); err == nil {
		t.Fatal("ReadArtifact allowed a path outside the workspace")
	}
}

func TestReadArtifactRejectsTooLarge(t *testing.T) {
	ws := t.TempDir()
	big := strings.Repeat("x", maxArtifactBytes+1)
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("big.txt"); err == nil {
		t.Fatal("ReadArtifact returned an over-sized file instead of erroring")
	}
}

func TestReadArtifactRejectsBinary(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "b.bin"), []byte{0x00, 0x01, 0xff, 0xfe}, 0o600); err != nil {
		t.Fatal(err)
	}
	a := appWithWorkspace(t, ws)
	if _, err := a.ReadArtifact("b.bin"); err == nil {
		t.Fatal("ReadArtifact returned binary content instead of erroring")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/desktop/ -run 'TestReadArtifact' -v`
Expected: FAIL — `a.ReadArtifact undefined` and `undefined: maxArtifactBytes`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/desktop/preview.go`:

```go
package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxArtifactBytes caps how large a text artifact ReadArtifact returns, so a giant
// file cannot lock up the renderer. Larger files are opened externally instead.
const maxArtifactBytes = 2 << 20 // 2 MiB

// ReadArtifact returns the UTF-8 text of a workspace file for React-rendered
// previews (Markdown/code/text). It rejects paths outside the workspace, files over
// maxArtifactBytes, and binary (non-UTF-8) content.
func (a *App) ReadArtifact(relPath string) (string, error) {
	a.mu.Lock()
	ws := a.workspace
	a.mu.Unlock()
	if ws == "" {
		return "", errors.New("no active workspace")
	}
	full := filepath.Join(ws, filepath.FromSlash(relPath))
	rel, err := filepath.Rel(ws, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path is outside the workspace")
	}
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		if r, err := filepath.Rel(ws, resolved); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", errors.New("path resolves outside the workspace")
		}
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", err
	}
	if info.Size() > maxArtifactBytes {
		return "", errors.New("file too large to preview")
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("file is not text")
	}
	return string(data), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/desktop/ -run 'TestReadArtifact' -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/preview.go internal/desktop/preview_test.go
git commit -m "Desktop: add ReadArtifact binding for text preview content

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Wire the preview server into the App lifecycle + expose the base URL

**Files:**
- Modify: `internal/desktop/app.go` (add a `preview *previewServer` field; start in `buildAndSetLocked`, stop in `closeLocked`; populate `SessionInfo.PreviewBaseURL` in `statusLocked`)
- Modify: `internal/desktop/types.go` (add `SessionInfo.PreviewBaseURL string`)
- Test: `internal/desktop/preview_lifecycle_test.go`

**Interfaces:**
- Consumes: `previewServer` (Task 1).
- Produces: `(*App).startPreview(workspace string)` / `(*App).stopPreview()` (called from the locked lifecycle); `SessionInfo.PreviewBaseURL` carries the URL to the frontend; `(*App).previewBaseURL() string`.

- [ ] **Step 1: Write the failing test**

Create `internal/desktop/preview_lifecycle_test.go`:

```go
package desktop

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestStartPreviewServesThenStops(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "x.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(&recordingSink{})

	a.startPreview(ws)
	base := a.previewBaseURL()
	if base == "" {
		t.Fatal("previewBaseURL empty after startPreview")
	}
	resp, err := http.Get(base + "x.txt")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("serve after start = %v %v", resp, err)
	}
	resp.Body.Close()

	a.stopPreview()
	if a.previewBaseURL() != "" {
		t.Fatal("previewBaseURL not cleared after stopPreview")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/desktop/ -run 'TestStartPreviewServesThenStops' -v`
Expected: FAIL — `a.startPreview undefined`.

- [ ] **Step 3: Write the lifecycle helpers**

Add to `internal/desktop/preview.go`:

```go
// startPreview (re)starts the workspace preview server. It replaces any running
// one so a workspace switch is clean. Failures are non-fatal (previews of
// text-based types still work via ReadArtifact).
func (a *App) startPreview(workspace string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
	if workspace == "" {
		return
	}
	ps := newPreviewServer()
	url, err := ps.start(workspace)
	if err != nil {
		return
	}
	a.preview = ps
	a.previewURL = url
}

func (a *App) stopPreview() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
}

func (a *App) previewBaseURL() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.previewURL
}
```

- [ ] **Step 4: Add the App fields**

In `internal/desktop/app.go`, add to the `App` struct (near `workspace`):

```go
	preview    *previewServer
	previewURL string
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/desktop/ -run 'TestStartPreviewServesThenStops' -v`
Expected: PASS.

- [ ] **Step 6: Wire into the session lifecycle**

In `internal/desktop/app.go`, inside `buildAndSetLocked`, after `a.workspace` is known and the session is built successfully (just before `return a.statusLocked(), nil`), the caller already holds `a.mu`; call the server start via a non-locking inner form. To avoid re-locking, factor the body: replace the `startPreview` mutex use here by starting the server inline under the held lock. Add just before `return a.statusLocked(), nil`:

```go
	// Restart the workspace preview server for this session (non-fatal on error).
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
	if ps := newPreviewServer(); cfg.CWD != "" {
		if url, err := ps.start(cfg.CWD); err == nil {
			a.preview = ps
			a.previewURL = url
		}
	}
```

In `closeLocked` (the teardown that runs under `a.mu`), add:

```go
	if a.preview != nil {
		a.preview.stop()
		a.preview = nil
		a.previewURL = ""
	}
```

- [ ] **Step 7: Add PreviewBaseURL to SessionInfo**

In `internal/desktop/types.go`, add to `SessionInfo`:

```go
	// PreviewBaseURL is the loopback static-server root for previewing workspace
	// files (empty if the server could not start). e.g. "http://127.0.0.1:52713/".
	PreviewBaseURL string `json:"previewBaseURL"`
```

In `internal/desktop/app.go`, in `statusLocked` (the method that builds `SessionInfo`), set the field:

```go
		PreviewBaseURL: a.previewURL,
```

- [ ] **Step 8: Run all desktop tests + build**

Run: `go build ./... && go test ./internal/desktop/ -run 'Preview|ReadArtifact|Start' -v`
Expected: PASS; build exit 0.

- [ ] **Step 9: Commit**

```bash
git add internal/desktop/app.go internal/desktop/types.go internal/desktop/preview.go internal/desktop/preview_lifecycle_test.go
git commit -m "Desktop: run a preview server per session and expose its base URL

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Frontend preview classification + URL helpers (pure)

**Files:**
- Create: `cmd/runcode-desktop/frontend/src/preview.ts`
- Test: `cmd/runcode-desktop/frontend/src/preview.test.ts`

**Interfaces:**
- Produces:
  - `type PreviewKind = 'markdown' | 'image' | 'svg' | 'html' | 'code' | 'text' | 'unsupported'`
  - `classifyPreview(path: string): { kind: PreviewKind; lang?: string }`
  - `isPreviewable(path: string): boolean` (kind !== 'unsupported')
  - `previewSrc(baseURL: string, relPath: string, bust?: number): string` (server URL for html/image, with an optional cache-busting `?v=` for refresh)
  - `toWorkspaceRel(path: string, cwd: string): string` — normalizes a tool-reported path (which may be absolute under `cwd`, or already workspace-relative) to a forward-slash workspace-relative path, so it is safe to pass to `ReadArtifact`/`previewSrc`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/runcode-desktop/frontend/src/preview.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { classifyPreview, isPreviewable, previewSrc } from './preview'

describe('classifyPreview', () => {
  it('maps by extension, case-insensitively', () => {
    expect(classifyPreview('README.md').kind).toBe('markdown')
    expect(classifyPreview('a/b/index.HTML').kind).toBe('html')
    expect(classifyPreview('logo.PNG').kind).toBe('image')
    expect(classifyPreview('icon.svg').kind).toBe('svg')
    expect(classifyPreview('notes.txt').kind).toBe('text')
  })
  it('classifies code with a highlight language', () => {
    expect(classifyPreview('main.go')).toEqual({ kind: 'code', lang: 'go' })
    expect(classifyPreview('app.tsx')).toEqual({ kind: 'code', lang: 'tsx' })
  })
  it('returns unsupported for unknown/binary', () => {
    expect(classifyPreview('archive.zip').kind).toBe('unsupported')
    expect(classifyPreview('noext').kind).toBe('unsupported')
  })
})

describe('isPreviewable', () => {
  it('is false for unsupported', () => {
    expect(isPreviewable('a.md')).toBe(true)
    expect(isPreviewable('a.zip')).toBe(false)
  })
})

describe('previewSrc', () => {
  it('joins base and path and adds a cache-buster', () => {
    expect(previewSrc('http://127.0.0.1:9/', 'a/b.html')).toBe('http://127.0.0.1:9/a/b.html')
    expect(previewSrc('http://127.0.0.1:9/', 'a b.png', 7)).toBe('http://127.0.0.1:9/a%20b.png?v=7')
  })
})

describe('toWorkspaceRel', () => {
  it('strips the workspace prefix from an absolute path', () => {
    expect(toWorkspaceRel('D:\\ws\\src\\a.ts', 'D:\\ws')).toBe('src/a.ts')
    expect(toWorkspaceRel('/home/u/ws/a.md', '/home/u/ws')).toBe('a.md')
  })
  it('leaves an already-relative path alone (normalizing slashes)', () => {
    expect(toWorkspaceRel('src\\a.ts', 'D:\\ws')).toBe('src/a.ts')
    expect(toWorkspaceRel('a.md', '/home/u/ws')).toBe('a.md')
  })
})
```

Update the import line at the top of the test file to include the new export:

```ts
import { classifyPreview, isPreviewable, previewSrc, toWorkspaceRel } from './preview'
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: FAIL — cannot resolve `./preview`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/runcode-desktop/frontend/src/preview.ts`:

```ts
export type PreviewKind = 'markdown' | 'image' | 'svg' | 'html' | 'code' | 'text' | 'unsupported'

const IMAGE = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico'])
const MARKDOWN = new Set(['md', 'markdown'])
const HTML = new Set(['html', 'htm'])
const TEXT = new Set(['txt', 'log', 'csv', 'env'])
// Extension -> highlight.js language for the code viewer.
const CODE: Record<string, string> = {
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'jsx',
  ts: 'typescript', tsx: 'tsx', py: 'python', go: 'go', rs: 'rust',
  java: 'java', c: 'c', h: 'c', cpp: 'cpp', cc: 'cpp', cs: 'csharp',
  css: 'css', scss: 'scss', json: 'json', yaml: 'yaml', yml: 'yaml',
  toml: 'toml', sh: 'bash', bash: 'bash', sql: 'sql', rb: 'ruby', php: 'php',
  kt: 'kotlin', swift: 'swift', xml: 'xml',
}

function ext(path: string): string {
  const base = path.replace(/\\/g, '/').split('/').pop() || ''
  const dot = base.lastIndexOf('.')
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : ''
}

export function classifyPreview(path: string): { kind: PreviewKind; lang?: string } {
  const e = ext(path)
  if (MARKDOWN.has(e)) return { kind: 'markdown' }
  if (e === 'svg') return { kind: 'svg' }
  if (IMAGE.has(e)) return { kind: 'image' }
  if (HTML.has(e)) return { kind: 'html' }
  if (CODE[e]) return { kind: 'code', lang: CODE[e] }
  if (TEXT.has(e)) return { kind: 'text' }
  return { kind: 'unsupported' }
}

export function isPreviewable(path: string): boolean {
  return classifyPreview(path).kind !== 'unsupported'
}

export function previewSrc(baseURL: string, relPath: string, bust?: number): string {
  const encoded = relPath.replace(/\\/g, '/').split('/').map(encodeURIComponent).join('/')
  const base = baseURL.endsWith('/') ? baseURL : baseURL + '/'
  return base + encoded + (bust ? `?v=${bust}` : '')
}

// toWorkspaceRel normalizes a tool-reported path to a forward-slash workspace-
// relative path. Tool events may carry either an absolute path under cwd or an
// already-relative one; both must become relative before ReadArtifact/previewSrc.
export function toWorkspaceRel(path: string, cwd: string): string {
  const p = path.replace(/\\/g, '/')
  const root = cwd.replace(/\\/g, '/').replace(/\/+$/, '')
  if (root && (p === root || p.startsWith(root + '/'))) {
    return p.slice(root.length + 1)
  }
  return p.replace(/^\.?\//, '')
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.ts cmd/runcode-desktop/frontend/src/preview.test.ts
git commit -m "Desktop UI: add preview file classification and URL helpers

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: File-tree helper for the browser (pure)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/preview.ts` (add `buildFileTree`)
- Modify: `cmd/runcode-desktop/frontend/src/preview.test.ts` (add tests)

**Interfaces:**
- Produces: `type FileNode = { name: string; path: string; dir: boolean; children?: FileNode[] }`; `buildFileTree(paths: string[]): FileNode[]` — a sorted (dirs first, then files) shallow tree from flat workspace-relative paths.

- [ ] **Step 1: Write the failing test**

Append to `cmd/runcode-desktop/frontend/src/preview.test.ts`:

```ts
import { buildFileTree } from './preview'

describe('buildFileTree', () => {
  it('nests by directory, dirs before files, sorted', () => {
    const tree = buildFileTree(['src/b.ts', 'src/a.ts', 'README.md', 'src/ui/x.css'])
    // top level: dir "src" before file "README.md"
    expect(tree.map((n) => n.name)).toEqual(['src', 'README.md'])
    const src = tree[0]
    expect(src.dir).toBe(true)
    // inside src: subdir "ui" before files a.ts, b.ts
    expect(src.children!.map((n) => n.name)).toEqual(['ui', 'a.ts', 'b.ts'])
    expect(src.children![1].path).toBe('src/a.ts')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: FAIL — `buildFileTree` is not exported.

- [ ] **Step 3: Write minimal implementation**

Append to `cmd/runcode-desktop/frontend/src/preview.ts`:

```ts
export type FileNode = { name: string; path: string; dir: boolean; children?: FileNode[] }

export function buildFileTree(paths: string[]): FileNode[] {
  type Dir = { node: FileNode; dirs: Map<string, Dir>; files: FileNode[] }
  const root: Dir = { node: { name: '', path: '', dir: true, children: [] }, dirs: new Map(), files: [] }
  for (const raw of paths) {
    const parts = raw.replace(/\\/g, '/').split('/').filter(Boolean)
    let cur = root
    for (let i = 0; i < parts.length; i++) {
      const isFile = i === parts.length - 1
      const path = parts.slice(0, i + 1).join('/')
      if (isFile) {
        cur.files.push({ name: parts[i], path, dir: false })
      } else {
        let child = cur.dirs.get(parts[i])
        if (!child) {
          child = { node: { name: parts[i], path, dir: true, children: [] }, dirs: new Map(), files: [] }
          cur.dirs.set(parts[i], child)
        }
        cur = child
      }
    }
  }
  const collect = (d: Dir): FileNode[] => {
    const dirs = [...d.dirs.values()].sort((a, b) => a.node.name.localeCompare(b.node.name))
    for (const sub of dirs) sub.node.children = collect(sub)
    const files = d.files.sort((a, b) => a.name.localeCompare(b.name))
    return [...dirs.map((x) => x.node), ...files]
  }
  return collect(root)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd cmd/runcode-desktop/frontend && npx vitest run src/preview.test.ts`
Expected: PASS (all preview tests).

- [ ] **Step 5: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.ts cmd/runcode-desktop/frontend/src/preview.test.ts
git commit -m "Desktop UI: add buildFileTree for the preview file browser

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Bridge bindings + PreviewBaseURL type

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/bridge.ts`

**Interfaces:**
- Consumes: backend `App.ReadArtifact` (Task 2); `SessionInfo.PreviewBaseURL` (Task 3).
- Produces: `readArtifact(relPath: string): Promise<string>`; `SessionInfo.previewBaseURL?: string` field on the existing type.

- [ ] **Step 1: Add the binding and type**

In `cmd/runcode-desktop/frontend/src/bridge.ts`, add near the other `app().X()` wrappers:

```ts
export const readArtifact = (relPath: string) => app().ReadArtifact(relPath) as Promise<string>
```

And add to the `SessionInfo` interface (next to `model`, `cwd`, ...):

```ts
  previewBaseURL?: string
```

- [ ] **Step 2: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`. (The generated Wails binding `App.ReadArtifact` exists after the next `wails build`; `app()` is `any`-typed so this compiles now.)

- [ ] **Step 3: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/bridge.ts
git commit -m "Desktop UI: bridge readArtifact and previewBaseURL

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 7: PreviewPanel + FileBrowser components

**Files:**
- Create: `cmd/runcode-desktop/frontend/src/preview.tsx`

**Interfaces:**
- Consumes: `classifyPreview`, `previewSrc`, `buildFileTree`, `FileNode` (Task 4/5); `readArtifact` (Task 6); the existing `Markdown` component (`./markdown`).
- Produces:
  - `PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void })`
  - `FileBrowser({ files, onPick }: { files: string[]; onPick: (path: string) => void })`

- [ ] **Step 1: Write the component**

Create `cmd/runcode-desktop/frontend/src/preview.tsx`:

```tsx
import { useEffect, useState } from 'react'
import { Markdown } from './markdown'
import { readArtifact } from './bridge'
import { classifyPreview, previewSrc, buildFileTree, type FileNode } from './preview'

// PreviewPanel renders one workspace artifact by type: HTML in a sandboxed iframe
// and images by the loopback static-server URL; Markdown/code/text by fetching the
// text via ReadArtifact and rendering it in React.
export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = relPath.replace(/\\/g, '/').split('/').pop() || relPath
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then(setText)
      .catch((e) => setErr(String(e)))
  }, [relPath, kind, bust, textual])

  return (
    <div className="flex flex-col h-full bg-surface">
      <div className="flex-none flex items-center gap-2 h-[52px] px-3 border-b border-line2">
        <span className="text-[13px] font-medium text-ink truncate flex-1 min-w-0" title={relPath}>{name}</span>
        <span className="text-[11px] text-faint">{kind}</span>
        <button className="text-muted hover:text-ink px-1.5" title="刷新" onClick={() => setBust((v) => v + 1)}>↻</button>
        {baseURL && <button className="text-muted hover:text-ink px-1.5" title="用系统程序打开" onClick={() => window.runtime.BrowserOpenURL(previewSrc(baseURL, relPath))}>↗</button>}
        <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={onClose}>✕</button>
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {(kind === 'code' || kind === 'text') && text != null && (
          <pre className="m-0 p-4 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words"><code className={lang ? `language-${lang}` : undefined}>{text}</code></pre>
        )}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。{baseURL && <button className="text-primaryink underline ml-1" onClick={() => window.runtime.BrowserOpenURL(previewSrc(baseURL, relPath))}>用系统程序打开</button>}</div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
      </div>
    </div>
  )
}

// FileBrowser lists the workspace as a shallow read-only tree; clicking a file
// asks to preview it (the parent decides if it is previewable).
export function FileBrowser({ files, onPick }: { files: string[]; onPick: (path: string) => void }) {
  const tree = buildFileTree(files)
  const render = (nodes: FileNode[], depth: number): React.ReactNode =>
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
  return <div className="text-[12.5px] py-1 overflow-auto h-full">{render(tree, 0)}</div>
}
```

- [ ] **Step 2: Add `BrowserOpenURL` to the Wails runtime type**

`cmd/runcode-desktop/frontend/src/wails.d.ts` already declares `window.runtime` as a non-optional object. Add `BrowserOpenURL` to that existing `runtime: { ... }` block (do NOT add a new `Window` interface):

```ts
    runtime: {
      EventsOn(name: string, callback: (data: unknown) => void): () => void
      EventsOff(name: string): void
      WindowMinimise(): void
      WindowToggleMaximise(): void
      WindowMaximise(): void
      WindowUnmaximise(): void
      Quit(): void
      BrowserOpenURL(url: string): void
    }
```

- [ ] **Step 3: Verify it builds**

Run: `cd cmd/runcode-desktop/frontend && npm run build`
Expected: `✓ built`.

- [ ] **Step 4: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/preview.tsx cmd/runcode-desktop/frontend/src/wails.d.ts
git commit -m "Desktop UI: add PreviewPanel and FileBrowser components

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 8: Wire the right-split panel + "预览" buttons into App.tsx

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/App.tsx`

**Interfaces:**
- Consumes: `PreviewPanel`, `FileBrowser` (Task 7); `isPreviewable` (Task 4); `listFiles` (existing bridge); `info.previewBaseURL` (Task 6).

- [ ] **Step 1: Import and add preview state**

At the top of `App.tsx`, add to the existing `./preview` import group (or a new import line):

```tsx
import { PreviewPanel, FileBrowser } from './preview'
import { isPreviewable, toWorkspaceRel } from './preview'
```

In the top-level component body, near the other `useState` hooks (e.g. by `const [files, setFiles]` around line 302), add:

```tsx
  const [previewPath, setPreviewPath] = useState<string | null>(null)
  const [browseOpen, setBrowseOpen] = useState(false)
```

- [ ] **Step 2: Add the preview pane as a right column beside `<main>`**

`<main>` (App.tsx ~line 937) is a `flex-col` holding the header and the view switch, and it sits in an outer flex row after `<Sidebar />`. Add the preview pane as a **sibling right after `</main>`** so it becomes a full-height right column (only in chat view). Find the `</main>` that closes the block opened at line 937 and insert immediately after it:

```tsx
        {view === 'chat' && (previewPath || browseOpen) && (
          <aside className="w-[44%] max-w-[720px] min-w-[320px] flex-none border-l border-line2 flex flex-col min-h-0 bg-surface">
            {previewPath ? (
              <PreviewPanel baseURL={info?.previewBaseURL ?? ''} relPath={previewPath} onClose={() => { setPreviewPath(null); setBrowseOpen(false) }} />
            ) : (
              <div className="flex flex-col h-full min-h-0">
                <div className="flex-none flex items-center h-[52px] px-3 border-b border-line2">
                  <span className="text-[13px] font-medium text-ink flex-1">文件预览</span>
                  <button className="text-muted hover:text-ink px-1.5" title="关闭" onClick={() => setBrowseOpen(false)}>✕</button>
                </div>
                <FileBrowser files={files} onPick={(p) => { if (isPreviewable(p)) setPreviewPath(toWorkspaceRel(p, info?.cwd ?? '')) }} />
              </div>
            )}
          </aside>
        )}
```

(`files` is the existing state populated by `listFiles`; its entries are already workspace-relative, so `toWorkspaceRel` is a harmless normalization there.)

- [ ] **Step 3: Add a "预览" button on previewable Write/Edit tool rows**

Add a `toolTargetPath` helper near `toolVerbTarget` (App.tsx ~line 159). It reuses the same path sources `toolVerbTarget` uses (`t.files[0].path` or the tool input's `path`):

```tsx
// toolTargetPath returns the file a Write/Edit acted on (absolute or workspace-
// relative), for wiring a preview affordance. Returns undefined when there is none.
function toolTargetPath(t: ToolEvent): string | undefined {
  const fromFiles = t.files?.[0]?.path
  if (fromFiles) return fromFiles
  const p = toolInputObj(t).path
  return typeof p === 'string' && p ? p : undefined
}
```

Give `ExecutionCard` an `onPreview` prop by changing its signature:

```tsx
function ExecutionCard({ tools, harmAllows, onPreview }: { tools: ToolEvent[]; harmAllows?: Record<string, string>; onPreview?: (rawPath: string) => void }) {
```

Inside the per-tool row, after the `allowReason` badge block (right before the `st === 'failed' ? …` status span), add the button:

```tsx
              {(t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed' && toolTargetPath(t) && isPreviewable(toolTargetPath(t)!) && (
                <button className="flex-none text-[10.5px] text-primaryink hover:underline px-1" title="预览" onClick={(e) => { e.stopPropagation(); onPreview?.(toolTargetPath(t)!) }}>预览</button>
              )}
```

Where `ExecutionCard` is rendered, pass `onPreview` so the click relativizes the path against the workspace and opens the panel. Find the `<ExecutionCard tools={…} harmAllows={harmAllows} />` render site and add the prop:

```tsx
<ExecutionCard tools={g.tools} harmAllows={harmAllows} onPreview={(raw) => setPreviewPath(toWorkspaceRel(raw, info?.cwd ?? ''))} />
```

(If the render site names the group differently than `g.tools` / `harmAllows`, keep those existing props and only add `onPreview`.)

- [ ] **Step 4: Add a header toggle to open the file browser**

In the chat `<header>` (App.tsx ~line 944, the `<div className="flex items-center gap-3">` control cluster), add a toggle button before `<WindowControls />`, wrapped in `NO_DRAG` so it stays clickable in the drag region:

```tsx
            <button style={NO_DRAG} className="text-muted hover:text-ink text-[12.5px] px-2" title="文件预览" onClick={() => { setBrowseOpen((v) => !v); setPreviewPath(null) }}>预览</button>
```

- [ ] **Step 5: Verify it builds + vitest still green**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npm run test`
Expected: `✓ built`; vitest all pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/App.tsx
git commit -m "Desktop UI: right-split preview panel, tool-card preview, file browser

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 9: Allow the loopback server in the WebView CSP + package & verify

**Files:**
- Modify: `cmd/runcode-desktop/frontend/index.html` (CSP meta) — confirm the actual CSP location; Wails may set CSP in `wails.json` or via the asset middleware. Check `cmd/runcode-desktop/wails.json` and `frontend/index.html` first.

**Interfaces:** none (configuration + manual verification).

- [ ] **Step 1: Find the current CSP**

Run: `grep -rn "Content-Security-Policy\|frame-src\|img-src" cmd/runcode-desktop/frontend/index.html cmd/runcode-desktop/wails.json`
If a CSP `<meta>` exists in `index.html`, edit it. If none exists, Wails' default WebView2 policy is permissive for loopback and this task may reduce to verification only — confirm HTML/image previews load in step 3.

- [ ] **Step 2: Ensure the CSP allows the loopback origin**

If a CSP meta tag is present, add `http://127.0.0.1:* http://localhost:*` to `frame-src`, `img-src`, and `media-src` (create the directives if absent). Example directive fragment:

```
frame-src 'self' http://127.0.0.1:* http://localhost:*; img-src 'self' data: http://127.0.0.1:* http://localhost:*;
```

- [ ] **Step 3: Package and manually verify**

Run: `cd cmd/runcode-desktop && wails build`
Then launch `build/bin/XRUN.exe`, open a workspace, and verify:
- Ask the agent to write `demo.md`, `demo.html` (with an inline `<img src="./logo.png">` plus a `logo.png`), and `demo.py`; each Write card shows a **预览** button.
- Clicking **预览** opens the right panel: Markdown renders, HTML renders **with its image resolved** (proves the static server), Python shows highlighted.
- The header **预览** button opens the file browser; clicking a file previews it; the refresh (↻) re-reads after the agent edits the file.
- Resize the window narrow — panel stays usable.

- [ ] **Step 4: Commit**

```bash
git add cmd/runcode-desktop/frontend/index.html
git commit -m "Desktop: allow the loopback preview server in the WebView CSP

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Notes for the implementer

- **Reuse over rebuild:** the `Markdown` component (`./markdown`) and highlight.js styles already exist — do not add a new markdown/highlight library.
- **Tool target path (Task 8, Step 3):** `toolTargetPath` reuses the exact sources `toolVerbTarget` already reads (`t.files?.[0]?.path`, else `toolInputObj(t).path`). The raw value may be absolute; `toWorkspaceRel(raw, info.cwd)` normalizes it before it reaches `ReadArtifact`/`previewSrc`. `t.type === 'completed'` is the real "done" discriminator (`ToolEvent.type` union in `bridge.ts`).
- **CSP (Task 9):** Wails WebView2 is generally permissive to `http://127.0.0.1:*` by default; only tighten/loosen if HTML/image previews fail to load. Prefer the least-privilege directive that works.
- **Locking (Task 3):** `startPreview`/`stopPreview` take `a.mu`; the copies inlined into `buildAndSetLocked`/`closeLocked` deliberately do **not** re-lock, because those methods already hold `a.mu` (re-locking a `sync.Mutex` would deadlock).
