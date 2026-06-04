# Changelog

All notable changes to runcode will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project intends to follow [Semantic Versioning](https://semver.org/) from v1.0 onward.

## [Unreleased]

### Added

- Cobra CLI entry point with `version` and a minimal provider-backed `chat` command.
- TOML configuration files: project-level `runcode.toml` (discovered by walking up from the working directory) and user-level `config.toml` (under `os.UserConfigDir()/runcode/`), with precedence flag > env > project file > user file > default. Credentials (`api_key`/`auth_token`) are only honored from the user-level file; any in a project file are ignored.
- `runcode config` command that prints the effective configuration, the loaded config-file paths, and whether credentials are set (credential values are never printed).
- Full session history persistence to `<workspace>/.runcode/sessions/<id>.jsonl` (one complete `llm.Message` per line), enabled by default. `--resume <id>` restores and continues a saved session; `--continue` resumes the most recent one; `--no-session` (or `RUNCODE_SESSION_PERSIST=off`) disables persistence. The on-disk history is append-only and loss-less, kept separate from the sanitized transcript.
- Context compaction: with `--max-context-tokens` / `ANTHROPIC_MAX_CONTEXT_TOKENS` / `max_context_tokens` set, once a turn's input tokens approach the budget the oldest turns are summarized into a single leading message (via the provider) while recent turns are kept verbatim. Compaction is incremental: an existing summary is carried forward verbatim and only the newly aged-out turns are summarized and appended, so already-condensed facts never pass through the model a second time (repeated re-summarization is what silently drops earlier facts). The summary is only fully re-summarized once its body outgrows a character budget derived from the token budget. Compaction touches only the in-memory working set; the on-disk history stays complete and can always recompute it.
- `pkg/llm` `Message`/`ContentBlock`/`ImageSource` now carry JSON tags so session history serializes to a stable, compact format that round-trips faithfully.
- `chat --loop` for process-local in-memory multi-turn sessions, with `/clear` to reset loop history.
- Provider-neutral LLM model in `pkg/llm`, including messages, content blocks, tool specs, streams, usage, and cache-control hints.
- Anthropic streaming provider skeleton using the official Anthropic Go SDK.
- OpenAI-compatible streaming provider (`--provider openai` / `RUNCODE_PROVIDER=openai`) that speaks the Chat Completions wire protocol directly over HTTP/SSE — no vendor SDK — so it also drives compatible endpoints (vLLM, Ollama, llama.cpp, LM Studio, and gateways such as a self-hosted qwen). It maps neutral messages to OpenAI roles (system merge, `tool_use` → `tool_calls`, `tool_result` → `tool` messages, images → `image_url`), reassembles streamed `content`/`tool_calls` deltas into block-structured events, and reports usage and stop reasons. The bearer credential is optional so unauthenticated local endpoints work; `--base-url` should point at the API root that serves `/chat/completions`.
- Prompt cache hints are now derived from the provider: `repl.NewSession` reads `llm.Provider.Capabilities().SupportsCacheControl` and the system-prompt assembler marks static sections with ephemeral cache control only when the provider supports it. Anthropic keeps its prompt caching; OpenAI-compatible providers no longer get no-op cache metadata. (Automatic compaction-budget inference from a provider's context window remains future work, since the capability is currently sourced from the CLI flag and cannot be derived for arbitrary compatible-endpoint model names.)
- Provider request retries: transient failures during connection setup (network errors, 429, and 5xx/529) are retried with capped exponential backoff that honors a numeric `Retry-After`. The OpenAI-compatible provider retries inside its HTTP transport — only the request-establishment phase, never mid-stream once the body is flowing — and the Anthropic provider enables the SDK's built-in retries. A cancelled/timed-out context and non-retryable client errors (4xx other than 429) are surfaced immediately. The retry count is configurable via `--max-retries` / `RUNCODE_MAX_RETRIES` / `max_retries` (0 = default, negative = disabled).
- Public tool SDK boundary in `pkg/tool`, including tool context, schema, events, result content, metadata, and `is_error` support.
- Built-in tools registered through `tools.Builtins()`:
  - `Read`
  - `Write`
  - `Edit`
  - `Glob`
  - `Grep`
  - `Bash`
  - `TodoWrite`
  - `WebFetch`
- `Read` tool with line-numbered output, offset/limit support, output bounds, and complete/partial read metadata.
- `Write` tool for workspace file creation and fresh-read overwrites.
- `Edit` tool for fresh-read exact string replacement and replace-all edits.
- `Glob` tool for workspace file discovery with slash glob semantics and `**` support.
- `Grep` tool for workspace regexp search with path/glob filtering and bounded output.
- Minimal `Bash` tool with workspace cwd, no stdin, timeout, stdout/stderr capture, output truncation, and `is_error` result semantics for non-zero exit or timeout.
- `TodoWrite` tool: the model records its current task list by passing the complete list each call (replacing the previous one). Each item has content, a status (pending/in_progress/completed), and an activeForm; the tool validates the list (non-empty, valid statuses, at most one in_progress), renders a checklist, and emits a progress event for the UI. It is side-effect-free and classified as a new `manage` permission operation that is allowed without approval (no files or commands). Registered in `tools.Builtins()` and exposed to the model and prompt automatically.
- `WebFetch` tool: fetches an http(s) URL (GET, with timeout, a 5 MiB body cap, and a redirect limit that refuses non-http(s) schemes) and returns its text — HTML is reduced to plain text (script/style/head dropped, block elements becoming line breaks), other text/JSON/XML is returned as-is, and binary content is rejected. It is modeled as a new `network` permission operation that always requires approval (so safe mode denies it); the approval shows the target host (never the full URL), and "allow for session/project" can be granted per host. Verified live against `example.com`.
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
- TUI slash commands are now driven by an extensible registry: each command (name, aliases, summary, handler) is registered in one place, and parsing, alias resolution, dispatch, and help text are all generated from it — adding a command no longer means editing a parse switch, a dispatch switch, and a hand-written help string. Built-ins are `/help`, `/clear`, `/status`, `/mode`, `/compact`, `/cost`, and `/exit` (alias `/quit`); the registry is the base for future `/model`.
- `/compact` slash command: summarizes the oldest turns of the current conversation on demand (regardless of the token budget) and reports the before/after message count, backed by a new `Session.Compact` and a `Compact` service method. Only the in-memory working set is touched; the on-disk session log stays complete. Available in the TUI.
- `/cost` slash command and token cost tracking: cumulative input/output tokens are summed across the session's turns; `/cost` shows them and, when `--input-price`/`--output-price` (or `input_price`/`output_price` in config, per million tokens) are set, an estimated cost. Compatible endpoints have no standard pricing, so it reports tokens only until prices are configured.
- `/mode` slash command for runtime permission-mode switching: `/mode safe|interactive` flips approval behavior without restarting. `permissions.Service` is now mode-aware — an interactive authorizer is always built (even when starting in safe mode) so safe can be widened to interactive at runtime, and concurrency/approval availability track the current mode. The status line reflects the new mode. Switching to interactive requires an approver; sensitive actions are still approved individually. Available in the TUI.
- TUI slash command menu: typing `/` opens a menu of matching commands above the input (generated from the registry, so it always lists exactly the available commands). It filters as you type; arrow keys navigate, Tab completes the name ready for arguments, Enter runs the highlighted command, and Esc closes it.
- Interactive approval prompt on stderr for `--permission-mode interactive` / `confirm`.
- TUI permission modal for `--permission-mode interactive`, with allow once / allow for session / deny choices, keyboard selection, sanitized request details (tool, operation, risk, workspace-relative targets, command classification), and serialized prompts.
- Session-scope permission memory: choosing "allow for session" stops re-prompting for equivalent actions (per mutation target for Write/Edit, per command classification for Bash) for the lifetime of the process session; available in both the TUI and the CLI interactive prompt.
- Persistent permission grants: interactive approval now offers "allow for project" alongside allow once / allow for session. Project grants are written to `<workspace>/.runcode/permissions.json` (0600, gitignored) and honored across processes, keyed by the same per-mutation-target / per-command-classification grain. The file also carries a denylist that is checked before prompting — a denied action is rejected without asking, and a deny always wins over an allow. A corrupt permissions file fails fast rather than silently dropping denylist rules. Available in both the TUI modal (`[p] allow project`) and the CLI prompt (`[p]roject`).
- Rich tool output in the TUI: tool progress cards now show a bounded, sanitized output excerpt — Bash stdout/stderr (with exit/duration envelope), Grep match lines, and a Read preview — expandable with `ctrl+o`. Output is display-only and never recorded to telemetry or transcripts.
- Full unified line diff for `Edit` and `Write`, computed by a new bounded `internal/diff` package and rendered with added/removed/context styling in the tool card; binary or oversized files fall back to a summary line.
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
- MCP integration.
- Hooks.
- Slash commands.
- Sub-agents.
- Skills.
- Context compaction and token/cost management.
