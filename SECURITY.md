# Security Policy

## Supported versions

runcode is currently pre-1.0. Security fixes are applied to the `main` branch and the latest tagged alpha release when practical.

| Version | Supported |
|---------|-----------|
| main | Yes |
| v0.x latest | Best effort |
| older v0.x | No |

## Reporting a vulnerability

Please do **not** open a public GitHub issue for security vulnerabilities.

Until a dedicated security mailbox is configured, report privately to the maintainer via GitHub. After the repository owner is finalized, replace this section with a dedicated email address and optional GPG key.

Include:

- Affected version or commit
- Operating system and shell / terminal
- Steps to reproduce
- Expected and actual impact
- Whether credentials, files, or network access are involved

## Scope

Security-sensitive areas include:

- Tool execution (`Bash`, `Write`, `Edit`, future MCP tools)
- Permission decisions and bypass mode
- Config, hooks, and plugin loading
- API keys and provider credentials
- Transcript and SQLite persistence
- Prompt injection paths from external content

## Transcript privacy

Transcript recording is disabled by default. When explicitly enabled, JSONL transcript files are written under the workspace `.runcode/transcripts` directory and may contain user prompts, final assistant text, and Bash command strings. Transcript records intentionally omit system prompts, provider credentials, base URLs, generic tool raw input, and full tool output.

## Disclosure process

1. Maintainers acknowledge receipt as soon as possible.
2. Maintainers reproduce and assess severity.
3. A fix is prepared privately when needed.
4. A release is published.
5. A public advisory is created with credit to the reporter, if desired.

## Safe harbor

Good-faith security research is welcome when it avoids:

- Destructive actions
- Data exfiltration beyond what is needed to prove impact
- Denial of service
- Accessing third-party accounts or systems without authorization

If in doubt, ask before testing.
