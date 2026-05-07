# runcode Architecture

This document will track the Go architecture as it is implemented.

The approved implementation plan lives in the Claude Code plan file for this session and will be distilled here as development progresses.

## Current status

v0.1 scaffold only:

- Cobra entry point
- Open-source repository metadata
- CI / lint / release configuration
- Empty package layout for the planned architecture

## Planned package layout

```text
cmd/runcode/           CLI entry
internal/app/          Bubble Tea model/update/view
internal/repl/         ReAct controller and tool executor
internal/permissions/  Permission modes and rules
internal/prompt/       System prompt assembler and cache boundary
internal/persistence/  SQLite, settings, RUNCODE.md loader
pkg/tool/              Public tool SDK
pkg/llm/               Public LLM provider abstraction
tools/                 Built-in tool implementations
prompts/               Embedded prompt templates
```
