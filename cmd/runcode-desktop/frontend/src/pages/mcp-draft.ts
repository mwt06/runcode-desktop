// MCP 编辑草稿与线上格式之间的双向映射：表单里是多行文本(每行一个参数 /
// KEY=VALUE)，wire 上是数组与字典。纯函数，与 React 无关，可单测。
import { type MCPServerInfo, type MCPServerInput } from '@/core/bridge'

export type MCPDraft = {
  originalName: string
  name: string
  transport: string
  command: string
  argsText: string
  envText: string
  dir: string
  url: string
  headersText: string
  passport: boolean
  enabled: boolean
}

export function kvToText(m?: Record<string, string> | null): string {
  if (!m) return ''
  return Object.entries(m).map(([k, v]) => `${k}=${v}`).join('\n')
}

export function textToKV(t: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of t.split('\n')) {
    const s = line.trim()
    if (!s) continue
    const i = s.indexOf('=')
    if (i <= 0) continue
    out[s.slice(0, i).trim()] = s.slice(i + 1).trim()
  }
  return out
}

export function linesToArr(t: string): string[] {
  return t.split('\n').map((l) => l.trim()).filter(Boolean)
}

// draftFrom seeds the editor: an existing server's fields, or the defaults for a
// brand-new stdio server when called with nothing.
export function draftFrom(s?: MCPServerInfo): MCPDraft {
  return {
    originalName: s?.name ?? '',
    name: s?.name ?? '',
    transport: s?.transport ?? 'stdio',
    command: s?.command ?? '',
    argsText: (s?.args ?? []).join('\n'),
    envText: kvToText(s?.env),
    dir: s?.dir ?? '',
    url: s?.url ?? '',
    headersText: kvToText(s?.headers),
    passport: s?.passport ?? false,
    enabled: s?.enabled ?? true,
  }
}

// toServerInput maps the draft back to the save request, trimming the free-text
// fields and parsing the multi-line ones.
export function toServerInput(draft: MCPDraft): MCPServerInput {
  return {
    originalName: draft.originalName,
    name: draft.name.trim(),
    transport: draft.transport,
    command: draft.command.trim(),
    args: linesToArr(draft.argsText),
    env: textToKV(draft.envText),
    dir: draft.dir.trim(),
    url: draft.url.trim(),
    headers: textToKV(draft.headersText),
    passport: draft.passport,
    enabled: draft.enabled,
  }
}
