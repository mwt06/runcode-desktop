import { describe, expect, it } from 'vitest'
import { parseMcpToolName, toolLabel, toolVerb } from './tool-catalog'

describe('parseMcpToolName', () => {
  it('splits the mcp__<server>__<tool> convention', () => {
    expect(parseMcpToolName('mcp__oa__my_todo')).toEqual({ server: 'oa', tool: 'my_todo' })
  })

  it('keeps underscores inside the tool name', () => {
    expect(parseMcpToolName('mcp__oa__request_detail')?.tool).toBe('request_detail')
  })

  it('returns null for non-MCP or malformed names', () => {
    expect(parseMcpToolName('Bash')).toBeNull()
    expect(parseMcpToolName('mcp__oa')).toBeNull()
    expect(parseMcpToolName('mcp____tool')).toBeNull()
    expect(parseMcpToolName('mcp__oa__')).toBeNull()
  })
})

describe('toolVerb', () => {
  it('uses the built-in catalog first', () => {
    expect(toolVerb('Read')).toBe('读取文件')
  })

  it('gives a platform MCP tool its Chinese name', () => {
    expect(toolVerb('mcp__oa__request_detail')).toBe('流程详情')
    expect(toolVerb('mcp__oa__my_todo')).toBe('待办事项')
  })

  it('falls back to the bare tool name, never the mcp__ plumbing', () => {
    expect(toolVerb('mcp__oa__brand_new_tool')).toBe('brand_new_tool')
    expect(toolVerb('mcp__other__do_thing')).toBe('do_thing')
  })

  it('leaves unknown non-MCP tools alone and labels a nameless event', () => {
    expect(toolVerb('CustomTool')).toBe('CustomTool')
    expect(toolVerb()).toBe('工具')
  })
})

describe('toolLabel', () => {
  it('uses the built-in label, not the short verb', () => {
    expect(toolLabel('Edit')).toBe('编辑文件')
  })

  it('shares the MCP rules with toolVerb', () => {
    expect(toolLabel('mcp__oa__search_docs')).toBe('搜索文档')
    expect(toolLabel('mcp__oa__unknown_one')).toBe('unknown_one')
  })
})
