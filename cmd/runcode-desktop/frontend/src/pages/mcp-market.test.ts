import { describe, expect, it } from 'vitest'
import type { McpMarketEntry, MCPServerInfo } from '@/core/bridge'
import { installState, marketEntryToInput } from './mcp-market'

const oa: McpMarketEntry = {
  id: 'oa',
  name: 'OA 办公助手',
  description: '十五个只读工具',
  transport: 'http',
  url: 'http://123.249.111.75:8101/mcp',
  passport: true,
  official: true,
}

// A server as it would appear installed from the oa entry; override to simulate
// drift (old install without passport, changed url, different name).
const installed = (over: Partial<MCPServerInfo> = {}): MCPServerInfo => ({
  name: 'oa',
  transport: 'http',
  url: oa.url,
  passport: true,
  enabled: true,
  connected: false,
  toolCount: 0,
  ...over,
})

describe('marketEntryToInput', () => {
  it('maps id to the server name and carries transport/url', () => {
    const input = marketEntryToInput(oa)
    expect(input.name).toBe('oa')
    expect(input.transport).toBe('http')
    expect(input.url).toBe('http://123.249.111.75:8101/mcp')
    expect(input.enabled).toBe(true)
    expect(input.originalName).toBe('')
  })

  it('carries the passport flag through so the token gets injected', () => {
    expect(marketEntryToInput(oa).passport).toBe(true)
    expect(marketEntryToInput({ ...oa, passport: false }).passport).toBe(false)
    expect(marketEntryToInput(oa).headers).toEqual({})
  })

  it('defaults transport to http when the entry omits it', () => {
    expect(marketEntryToInput({ ...oa, transport: '' }).transport).toBe('http')
  })
})

describe('installState', () => {
  it('none when no installed server has the entry id', () => {
    expect(installState(oa, [])).toBe('none')
    expect(installState(oa, [installed({ name: 'other' })])).toBe('none')
  })

  it('installed when present and url+passport+transport all match the market', () => {
    expect(installState(oa, [installed()])).toBe('installed')
  })

  it('outdated when present but drifted from the market entry', () => {
    // Installed before it carried passport (the exact bug an old install hits).
    expect(installState(oa, [installed({ passport: false })])).toBe('outdated')
    // Or the market changed the url out from under a stale install.
    expect(installState(oa, [installed({ url: 'http://old/mcp' })])).toBe('outdated')
  })
})
