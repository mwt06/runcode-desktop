# Open-Actions Fix (Cross-Platform) + Remove Card Rail — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the artifact card's "open with default app" work reliably on Mac/Windows/Linux (it's broken on Windows via `rundll32`), and remove the colored left rail from the card.

**Architecture:** Replace the unreliable Windows `rundll32` open with `explorer <path>` inside the existing `openCommand` GOOS switch (still shell-free, still workspace-bounded); reveal (`explorer /select` / `open -R` / `xdg-open <dir>`) and copy (`ResolveArtifactPath` + clipboard) are already correct and are confirmed by manual verification. Drop the card's `borderLeft` accent, keeping the colored type icon.

**Tech Stack:** Go (`os/exec`, standard library), React + TypeScript, vitest.

## Global Constraints

- Cross-platform: the app runs on **Mac / Windows / Linux** — every OS branch must have a working path; no OS-specific code without a cross-platform fallback.
- All OS-launch commands stay **shell-free** (`exec.Command` invoking a binary directly — no `cmd`/`sh`/`powershell`), preserving the filename command-injection fix. The `TestOpenCommandDoesNotUseShell` regression test must keep passing.
- Workspace-bounded: `OpenExternal`/`RevealInFolder`/`ResolveArtifactPath` reject out-of-workspace paths (via `resolveWithinWorkspace`) before launching — unchanged.
- No new Go or npm dependencies. Go 1.26; vite (esbuild, no tsc gate).
- End every Go commit message with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.

---

### Task 1: Cross-platform "open with default app" (backend)

**Files:**
- Modify: `internal/desktop/open.go` (`openCommand` Windows branch + doc comment)
- Modify: `internal/desktop/open_test.go` (add a Windows-open regression assertion)

**Interfaces:**
- Consumes: `resolveWithinWorkspace` (existing).
- Produces: `openCommand(path string) *exec.Cmd` — Windows now returns `exec.Command("explorer", path)` (was `rundll32`); macOS `open`, Linux `xdg-open` unchanged.

- [ ] **Step 1: Write/extend the failing test**

In `internal/desktop/open_test.go`, add a regression test that pins the Windows open command to `explorer` (not the reverted `rundll32`) and re-confirms no shell. Append:

```go
func TestOpenCommandWindowsUsesExplorer(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only open command")
	}
	cmd := openCommand(`C:\ws\a.md`)
	base := strings.ToLower(filepath.Base(cmd.Path))
	if !strings.HasPrefix(base, "explorer") {
		t.Fatalf("windows open should use explorer, got %q (args %v)", base, cmd.Args)
	}
	if strings.HasPrefix(base, "rundll32") {
		t.Fatal("windows open must not use rundll32 (unreliable for local files)")
	}
}
```

(`open_test.go` already imports `os`, `path/filepath`, `strings`, `os/exec`, `testing`. This new test also needs `runtime` — **add `"runtime"` to the import block** if it isn't there yet, or the build fails with "undefined: runtime".)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/desktop/ -run 'TestOpenCommandWindowsUsesExplorer' -v`
Expected: on Windows, FAIL — current `openCommand` returns `rundll32`, so `base` is `rundll32.exe`, not `explorer`. (On non-Windows it SKIPS — run on the Windows dev host to see RED.)

- [ ] **Step 3: Fix `openCommand` + the doc comment**

In `internal/desktop/open.go`, replace the `openCommand` function (currently the `rundll32` version) with:

```go
// openCommand builds the command to open path with the OS default app WITHOUT a
// shell, so an attacker-chosen filename (the workspace is AI-written) cannot inject
// commands — each binary is invoked directly with the path as an inert argv element.
// Windows uses explorer.exe (opens a file with its default handler); rundll32 was
// tried but is unreliable for local files. If explorer ever proves flaky for some
// type, switch to the ShellExecute API (build-tagged) — do NOT reintroduce cmd /c start.
func openCommand(path string) *exec.Cmd {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", path)
	case "darwin":
		return exec.Command("open", path)
	default:
		return exec.Command("xdg-open", path)
	}
}
```

(Leave `revealCommand`, `OpenExternal`, `RevealInFolder`, `ResolveArtifactPath`, `resolveArtifact` unchanged — they are already correct and shell-free.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/desktop/ -run 'TestOpenCommand|TestOpenBindings|TestResolveArtifactPath' -v`
Expected: PASS — `TestOpenCommandWindowsUsesExplorer` (runs on Windows), `TestOpenCommandDoesNotUseShell` (explorer/open/xdg-open are not shells), and the escape-rejection tests all green.

- [ ] **Step 5: Build + commit**

```bash
gofmt -w internal/desktop/ && go build ./...
git add internal/desktop/open.go internal/desktop/open_test.go
git commit -m "Desktop: open files with explorer on Windows (rundll32 was unreliable)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Remove the colored rail from ArtifactCard (frontend)

**Files:**
- Modify: `cmd/runcode-desktop/frontend/src/artifact-card.tsx`

**Interfaces:**
- Consumes: `kindAccent`/`kindIcon` (still used for the colored type icon).
- Produces: the card renders with a uniform border (no colored left rail); the colored type icon stays.

- [ ] **Step 1: Remove the left-rail style**

In `artifact-card.tsx`, the `ArtifactCard` root `<div>` currently has (line ~54-55):

```tsx
      style={{ borderLeftColor: accent, borderLeftWidth: 3 }}
      className={`group flex items-center gap-2.5 border border-line2 rounded-lg pl-3 pr-2.5 py-2 bg-surface ${previewable ? 'cursor-pointer hover:bg-surface2/40' : ''}`}
```

DELETE the `style={{ borderLeftColor: accent, borderLeftWidth: 3 }}` line entirely. Keep the `className` as-is (`border border-line2` gives a uniform 1px border on all sides once the accent override is gone). The result: a plain bordered card, no colored vertical bar.

`accent` (`const accent = kindAccent(kind)`) stays — it is still used by the type icon `<span style={{ color: accent }}>...`. Do NOT remove `accent` or the icon color.

- [ ] **Step 2: Verify build + tests**

Run: `cd cmd/runcode-desktop/frontend && npm run build && npx vitest run`
Expected: `✓ built`; vitest green (~37 passing). Grep to confirm `borderLeftColor` is gone from the file and `kindAccent`/`accent` are still used (by the icon).

- [ ] **Step 3: Commit**

```bash
git add cmd/runcode-desktop/frontend/src/artifact-card.tsx
git commit -m "Desktop UI: drop the colored left rail on artifact cards (keep the type icon)" -m "Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Package & manual verification

**Files:** none (build + manual).

- [ ] **Step 1: Full gate**

Run: `go build ./... && go test ./internal/desktop/... && (cd cmd/runcode-desktop/frontend && npm run build && npx vitest run) && go -C cmd/runcode-desktop build ./...`
Expected: all green.

- [ ] **Step 2: Package build**

Run: `cd cmd/runcode-desktop && wails build`
Expected: `cmd/runcode-desktop/build/bin/XRUN.exe` produced (Wails regenerates JS bindings).

- [ ] **Step 3: Manual verification (on the dev OS)**

Launch the built app, open a workspace, have the agent write a `demo.md`, then on its artifact card open the "打开方式" menu and confirm all three now work:
- **用系统默认程序打开** → the file opens in the OS default app (this was broken via rundll32; now uses `explorer`/`open`/`xdg-open`).
- **在文件夹中显示** → the OS file manager opens with the file located.
- **复制路径** → the absolute path is on the clipboard (paste to confirm).
- The card shows **no colored vertical rail**, but the **colored type icon** is still present.
If reveal or copy is still broken on your OS, note the exact symptom — reveal uses `explorer /select,` (a known Windows quirk with space-containing paths) and copy uses `ClipboardSetText` with a `navigator.clipboard` fallback; either can then be fixed as a focused follow-up.

- [ ] **Step 4: Commit (only if a config file changed; otherwise report results)**

---

## Notes for the implementer

- **`explorer <path>` on Windows** opens a FILE with its default app (opening a folder path would open the folder — but these are always files). explorer returns exit code 1 even on success; the bindings use `.Start()` (no wait), so that is fine.
- **Shell-free is a hard invariant** — never reintroduce `cmd /c start` (it was a confirmed RCE via AI-written filenames like `x&calc.exe`). `explorer`/`open`/`xdg-open` are invoked directly; a filename with `&` is inert argv.
- **Do not touch** `revealCommand`, the escape-rejection logic, the width guardrail, or the preview type-accent continuity (tab/header) — only the card's left rail is removed.
