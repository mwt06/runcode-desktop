import { describe, it, expect } from 'vitest'
import type { MCPServerInfo } from '@/core/bridge'
import { draftFrom, kvToText, linesToArr, textToKV, toServerInput } from './mcp-draft'

describe('kvToText / textToKV', () => {
  it('round-trips a KEY=VALUE map', () => {
    expect(textToKV(kvToText({ A: '1', B: '2' }))).toEqual({ A: '1', B: '2' })
  })
  it('renders nothing for an empty or missing map', () => {
    expect(kvToText(undefined)).toBe('')
    expect(kvToText(null)).toBe('')
    expect(kvToText({})).toBe('')
  })
  it('trims each side and keeps "=" inside the value', () => {
    expect(textToKV('  A = 1 \nB=x=y')).toEqual({ A: '1', B: 'x=y' })
  })
  it('drops blank lines and lines without a leading key', () => {
    expect(textToKV('\n\nA=1\n=novalue\nnokey\n')).toEqual({ A: '1' })
  })
})

describe('linesToArr', () => {
  it('trims lines and drops the empty ones', () => {
    expect(linesToArr('-y\n\n  @scope/pkg  \n')).toEqual(['-y', '@scope/pkg'])
    expect(linesToArr('')).toEqual([])
  })
})

describe('draftFrom', () => {
  it('defaults a brand-new server to an enabled stdio entry', () => {
    expect(draftFrom()).toEqual({
      originalName: '', name: '', transport: 'stdio', command: '',
      argsText: '', envText: '', dir: '', url: '', headersText: '', passport: false, enabled: true,
    })
  })
  it('seeds the editor from an existing server, joining args onto lines', () => {
    const s = {
      name: 'fs', transport: 'stdio', command: 'npx', args: ['-y', 'server-filesystem'],
      env: { TOKEN: '${T}' }, dir: '/w', url: '', headers: null, enabled: false,
    } as unknown as MCPServerInfo
    expect(draftFrom(s)).toEqual({
      originalName: 'fs', name: 'fs', transport: 'stdio', command: 'npx',
      argsText: '-y\nserver-filesystem', envText: 'TOKEN=${T}', dir: '/w', url: '',
      headersText: '', passport: false, enabled: false,
    })
  })
})

describe('toServerInput', () => {
  it('trims the text fields and parses the multi-line ones', () => {
    const input = toServerInput({
      originalName: 'old', name: '  fs  ', transport: 'stdio', command: '  npx ',
      argsText: '-y\n\n pkg ', envText: 'A=1', dir: ' /w ', url: '  ', headersText: '', passport: false, enabled: true,
    })
    expect(input).toEqual({
      originalName: 'old', name: 'fs', transport: 'stdio', command: 'npx',
      args: ['-y', 'pkg'], env: { A: '1' }, dir: '/w', url: '', headers: {}, passport: false, enabled: true,
    })
  })
  it('keeps originalName so a rename updates in place instead of duplicating', () => {
    expect(toServerInput({ ...draftFrom(), originalName: 'old', name: 'new' }).originalName).toBe('old')
  })
})
