# runcode-desktop

A Wails v2 desktop shell for runcode. It is a thin adapter over the core engine:
all session logic lives in [`internal/desktop`](../../internal/desktop) (no GUI
dependency, unit-tested), and this command supplies a Wails-backed event sink,
binds `desktop.App` to the frontend, and embeds the built web UI.

This is a **nested Go module** (its own `go.mod`) so the GUI's heavy/CGO/GTK
dependencies stay out of the core runcode module — the core's `go build ./...`
and CI never pull Wails. Its module path stays under `github.com/wt68/runcode`,
so it can still import the core's `internal/` packages.

## Prerequisites

- Go 1.26+, Node 18+ (this repo is tested with Node 24)
- A C compiler (mingw-w64 gcc on Windows; gcc/webkit2gtk on Linux)
- WebView2 runtime (built into Windows 11)
- Wails CLI:

  ```sh
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  wails doctor   # verify the toolchain
  ```

## Develop

From this directory (`cmd/runcode-desktop`):

```sh
wails dev
```

`wails dev` runs the Vite dev server with hot reload and the Go backend together.
Credentials/model are read from the environment when the start form leaves them
blank — e.g. set `ANTHROPIC_API_KEY` and `ANTHROPIC_MODEL` before launching, or
type them into the start form.

## Build

```sh
wails build            # produces build/bin/runcode-desktop(.exe)
```

`wails build` runs `npm install` + `npm run build` (see `wails.json`) to populate
`frontend/dist`, which `main.go` embeds via `//go:embed`.

## Architecture

```
frontend (React/Vite)
  │  window.go.desktop.App.*   (commands)
  │  window.runtime.EventsOn   (assistant:delta / tool:event / permission:request / turn:end|error / warning)
  ▼
cmd/runcode-desktop (main.go)   Wails shell: eventSink + Bind(app) + embed
  ▼
internal/desktop (App, Approver)  transport-agnostic session manager + async approval
  ▼
internal/engine (Build, Session)  the shared engine facade (also used by the CLI/TUI)
```

The async permission flow: a tool needing approval blocks inside the engine; the
`Approver` emits a `permission:request` event with an id and waits; the UI shows a
modal and calls `ResolvePermission(id, decision)`; the engine unblocks. Interrupt
and session close deny all pending requests so no goroutine leaks.
