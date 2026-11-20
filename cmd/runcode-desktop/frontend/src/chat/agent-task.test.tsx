// @vitest-environment jsdom
import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentTaskCard } from './agent-task'
import type { ToolEvent } from '@/core/bridge'

const SVG = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="5" height="5"/></svg>'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  vi.stubGlobal('URL', {
    ...URL,
    createObjectURL: (blob: Blob) => `blob:test-${blob.size}`,
    revokeObjectURL: () => {},
  })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

describe('AgentTaskCard inline preview', () => {
  it('expands finished task text through AssistantMarkdown (same path as main reply)', async () => {
    const tool = {
      type: 'completed',
      toolName: 'Task',
      toolUseID: 't1',
      input: {},
    } as ToolEvent
    await act(async () => {
      root.render(createElement(AgentTaskCard, {
        tool,
        nested: {
          agent: 'explore',
          text: '图如下\n\n```svg\n' + SVG + '\n```',
          tools: [],
        },
      }))
    })
    const expand = host.querySelector('button')
    expect(expand?.textContent).toMatch(/展开/)
    await act(async () => { expand?.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    // 走 InlinePreview：要么成功出图，要么带「未显示预览」回退——都不是裸 Markdown 静默源码。
    const html = host.innerHTML
    const viaPreview = html.includes('<img') || html.includes('未显示预览') || html.includes('min-h-12')
    expect(viaPreview).toBe(true)
  })
})
