# Changelog

All notable changes to runcode will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project intends to follow [Semantic Versioning](https://semver.org/) from v1.0 onward.

## [Unreleased]

### Added

- Cobra CLI entry point with `version` and a minimal provider-backed `chat` command.
- `chat --loop` for process-local in-memory multi-turn sessions, with `/clear` to reset loop history.
- Provider-neutral LLM model in `pkg/llm`, including messages, content blocks, tool specs, streams, usage, and cache-control hints.
- Anthropic streaming provider skeleton using the official Anthropic Go SDK.
- Public tool SDK boundary in `pkg/tool`, including tool context, schema, events, result content, metadata, and `is_error` support.
- Built-in tools registered through `tools.Builtins()`:
  - `Read`
  - `Write`
  - `Edit`
  - `Glob`
  - `Grep`
  - `Bash`
- `Read` tool with line-numbered output, offset/limit support, output bounds, and complete/partial read metadata.
- `Write` tool for workspace file creation and fresh-read overwrites.
- `Edit` tool for fresh-read exact string replacement and replace-all edits.
- `Glob` tool for workspace file discovery with slash glob semantics and `**` support.
- `Grep` tool for workspace regexp search with path/glob filtering and bounded output.
- Minimal `Bash` tool with workspace cwd, no stdin, timeout, stdout/stderr capture, output truncation, and `is_error` result semantics for non-zero exit or timeout.
- Finite ReAct session controller in `internal/repl` with tool-use iteration, tool-result conversion, max-iteration protection, in-memory history, and optional reasoning classification.
- Unified permission boundary in `internal/permissions`, including action/resource/risk modeling, resolver, policy, safe mode, interactive approval, command classification, and sanitized approval summaries.
- Permission-aware executor that records permission decisions and returns permission denial, unknown tools, and recoverable tool runtime errors as error tool results.
- Shared path resolution and fresh-read gates in `internal/toolpath`.
- Telemetry foundation in `internal/telemetry`, including event model, no-op recorder, JSONL recorder, bounded async recorder, memory recorder, and trace/turn/request IDs.
- `--telemetry off|jsonl` and `RUNCODE_TELEMETRY` support for CLI chat.
- System prompt assembler in `internal/prompt`, with static/dynamic cache boundary, tool descriptions, environment section, permission-mode guidance, memory/project context slots, and reasoning guidance.
- Project context loader for `RUNCODE.md` / `CLAUDE.md`, wired into `chat` prompt construction with bounded reads and truncation.
- Opt-in JSONL transcript recording with `--transcript jsonl` / `RUNCODE_TRANSCRIPT=jsonl` and optional `--session-id` / `RUNCODE_SESSION_ID`.
- Minimal Bubble Tea `runcode tui` MVP with a Claude Code-style bottom status area, cumulative context token and thinking-mode indicators, conversation viewport, single-line input with top and bottom dividers, streaming assistant Markdown rendering, tree-style tool progress cards with safe file summaries, and `/help`, `/clear`, `/status`, `/exit`.
- Interactive approval prompt on stderr for `--permission-mode interactive` / `confirm`.
- TUI permission modal for `--permission-mode interactive`, with allow once / allow for session / deny choices, keyboard selection, sanitized request details (tool, operation, risk, workspace-relative targets, command classification), and serialized prompts.
- Session-scope permission memory: choosing "allow for session" stops re-prompting for equivalent actions (per mutation target for Write/Edit, per command classification for Bash) for the lifetime of the process session; available in both the TUI and the CLI interactive prompt.
- Current implementation status document at `docs/implementation-status.md`.

### Changed

- Documentation now reflects the current minimal chat/session/tool/permission implementation instead of the original placeholder-only scaffold.
- `docs/data-flow-and-prompt.md` now describes the actual CLI -> Session -> provider -> tool -> permission flow.
- `docs/architecture.md` tracks the current implemented architecture, including `Bash` MVP and command permission classification.

### Fixed

- Tool result conversion preserves `is_error` semantics for permission denials, unknown tools, recoverable tool runtime errors, and Bash command failures.
- Permission denial tool results now include sanitized reason and final-effect details so the model can self-correct without exposing raw inputs.
- Shared line input avoids buffered stdin loss between `chat --loop` prompts and interactive approval prompts.
- Session history cloning deep-copies raw tool input and image data.

### Security

- Workspace containment is enforced for read/search/mutation tools, including symlink escape checks.
- Write/Edit mutations require fresh complete reads before overwriting or editing existing files.
- Permission telemetry and approval prompts avoid raw paths, raw commands, tool inputs, tool outputs, file contents, credentials, and URLs.
- Bash commands are classified before execution; unknown, privileged, destructive, outside-write, and complex shell-control commands are denied before approval.
- The default `safe` permission mode does not execute approval-requiring writes, edits, or Bash commands.
- Transcript records use a whitelist schema and omit system prompts, credentials, generic tool raw input, and full tool output; enabled transcripts may still contain user prompts, assistant text, and Bash command strings.

## [0.1.0-alpha] - TBD

### Planned

- Full TUI product features beyond the MVP (rich tool output, diff viewer, transcript browser, model switching).
- SQLite transcript and persistent session resume.
- OpenAI provider.
- TodoWrite tool.
- MCP integration.
- Hooks.
- Slash commands.
- Sub-agents.
- Skills.
- Context compaction and token/cost management.
