# Contributing to runcode

Thanks for your interest! `runcode` is in **alpha** — we welcome bug reports, design discussion, and code, but please read this first.

## Status disclaimer

- The project is pre-1.0. The `pkg/` API is **not stable** and may break in any minor release until v1.0.
- Large feature PRs without prior discussion are likely to be redirected. Please open an issue first.
- We follow the milestone roadmap in [README.md](./README.md). Work that doesn't fit the current milestone may be parked.

## Quick development setup

For development, check out the engine repo next to this one — the committed `go.work` links them so engine edits take effect live (builds without the sibling work too: `GOWORK=off GOPRIVATE=gitlab.ouc-online.com.cn` fetches the tagged engine directly):

```bash
git clone https://github.com/wt68/runcode.git
git clone https://gitlab.ouc-online.com.cn/aibase/agentloop.git agentloop   # sibling checkout (go.work links it)
cd runcode
go build ./cmd/runcode
go test -race ./...
```

Required:

- Go 1.26+
- (optional) `golangci-lint` for local linting: `golangci-lint run ./...`
- (optional) `goreleaser` for testing release builds: `goreleaser build --snapshot --clean`

## Code conventions

- Follow `gofmt` / `goimports` (CI enforces).
- Pass `go vet` and `golangci-lint run`.
- All public types in `pkg/` need doc comments (`// TypeName ...`).
- Tests live next to the code: `foo.go` ↔ `foo_test.go`. Aim for `-race` clean.
- Errors: wrap with `fmt.Errorf("context: %w", err)`. No naked `panic` in non-test code.
- Concurrency: prefer channels and `errgroup` over manual `sync` primitives where reasonable.

## Architectural rules

- `internal/` is **never** imported from outside the module. Don't expose internals via re-export.
- Engine work — tools, LLM providers, the permission model, persistence, the public engine API — belongs in the **agentloop** repository (`gitlab.ouc-online.com.cn/aibase/agentloop`). This repository takes shell work: CLI/TUI, the desktop app, and the server skeleton.

## Pull request process

1. Open an issue first for non-trivial work.
2. Fork → branch → push → PR.
3. Ensure CI is green: lint + tests on linux/macos/windows.
4. Reference the issue (`Closes #N`).
5. A maintainer reviews. We aim for first response within a week.

## Reporting bugs

Open an issue with:

- `runcode version` output
- OS + terminal emulator
- Minimal reproduction steps
- Expected vs actual behavior

For security issues, see [SECURITY.md](./SECURITY.md) — please do **not** open a public issue.

## License

By contributing, you agree your contributions are licensed under [Apache-2.0](./LICENSE).
