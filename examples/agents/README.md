# Example sub-agents

A **sub-agent** is a focused assistant the main agent delegates a self-contained
task to by calling the built-in `Task` tool. Each sub-agent runs its own ReAct
loop with its own system prompt, a restricted tool set, and optionally its own
model, then returns a single report.

These files are **examples/templates** — they are not loaded from here. To enable
one, copy it into a convention directory:

- **Project-level** (shared with your team, in-repo): `<workspace>/.runcode/agents/`
- **User-level** (personal, all projects): `<userConfigDir>/runcode/agents/`
  - Linux/macOS: `~/.config/runcode/agents/`
  - Windows: `%AppData%\runcode\agents\`

```sh
mkdir -p .runcode/agents
cp examples/agents/code-reviewer.md .runcode/agents/
```

Precedence is **user > project > builtin**, so a definition you add shadows a
same-named one. `runcode config` lists the sub-agents that are currently available
(user/project ones tagged).

## Definition format

Each sub-agent is a single `*.md` file: YAML-style frontmatter followed by the
system prompt body.

```markdown
---
name: code-reviewer            # required; letters, digits, '-' or '_'
description: One line shown ... # required; the catalog entry the main agent sees
tools: Read, Grep, Glob        # optional; omit or use "*" to inherit every tool
model: claude-opus-4-8         # optional; inherits the parent model otherwise
---

The system prompt for the sub-agent goes here. Its final message is returned
verbatim to the main agent as the task result.
```

Notes:

- **`tools`** is a comma- or space-separated allowlist. Omit it (or use `*`) to
  inherit every tool the main agent can delegate (builtins, MCP tools, and
  `Skill`). A sub-agent never receives the `Task` tool, so delegation is exactly
  one level deep.
- **`model`** changes only the model name; the provider and credentials are always
  inherited from the parent.
- Sub-agents share the parent's **permission mode**. A read-only agent
  (`Read`/`Grep`/`Glob`) works fully in `safe` mode; one that writes files or runs
  commands needs `interactive` mode (or an allowlist), since `Write`/`Edit`/`Bash`
  require approval.

## Files here

| File | Tools | Notes |
|------|-------|-------|
| `code-reviewer.md` | `Read`, `Grep`, `Glob` | Read-only; works in `safe` mode. |
| `test-writer.md` | inherits all | Writes/runs tests; use `interactive` mode. |
