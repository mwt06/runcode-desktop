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

  // 宿主注入的桌面工具与引擎的 MCP 内置工具都不带 mcp__ 前缀，走的是内置表；
  // 漏登记时会把英文原名直接漏进中文界面，这几条把它们钉住。
  it('covers the desktop host tools', () => {
    expect(toolVerb('ReadOffice')).toBe('读取文档')
    expect(toolVerb('open_preview')).toBe('预览')
  })

  it('covers the engine MCP built-ins, which carry no mcp__ prefix', () => {
    expect(toolVerb('ListMcpResources')).toBe('列出资源')
    expect(toolVerb('ReadMcpResource')).toBe('读取资源')
    expect(toolVerb('ListMcpPrompts')).toBe('列出提示词')
    expect(toolVerb('GetMcpPrompt')).toBe('取提示词')
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

  it('gives the host tools a full name for the management page', () => {
    expect(toolLabel('ReadOffice')).toBe('读取 Office 文档')
    expect(toolLabel('ListMcpResources')).toBe('列出 MCP 资源')
  })
})
