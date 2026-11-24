import { describe, expect, it } from 'vitest'
import type { Block } from '@/chat/blocks'
import type { ToolEvent } from '@/core/bridge'
import { collectAutoPreviewCandidates } from './auto-preview-candidates'

function writeTool(path: string): Block {
  return {
    kind: 'tool',
    id: 'w1',
    tool: {
      type: 'completed',
      toolName: 'Write',
      toolUseID: 'w',
      input: { path },
    } as ToolEvent,
  }
}

function imageGenTool(path: string): Block {
  return {
    kind: 'tool',
    id: 'ig1',
    tool: {
      type: 'completed',
      toolName: 'image_generate',
      toolUseID: 'ig',
      data: {
        version: 1,
        status: 'succeeded',
        images: [{ path, mimeType: 'image/png', bytes: 1, width: 1, height: 1 }],
      },
    } as ToolEvent,
  }
}

describe('collectAutoPreviewCandidates', () => {
  it('keeps pptx/md and Write products, drops 生成图片 paths from text', () => {
    const blocks: Block[] = [
      writeTool('notes/part.md'),
      {
        kind: 'assistant',
        id: 'a1',
        text: '已生成 deck.pptx 与 生成图片/gen_x/image.png 以及 notes/part.md',
        streaming: false,
        ts: '',
      },
    ]
    const got = collectAutoPreviewCandidates(blocks, 'D:\\ws')
    expect(got).toContain('deck.pptx')
    expect(got).toContain('notes/part.md')
    expect(got.some((p) => p.replace(/\\/g, '/').includes('生成图片/'))).toBe(false)
  })

  it('drops absolute paths that resolve under 生成图片/', () => {
    const blocks: Block[] = [{
      kind: 'assistant',
      id: 'a1',
      text: '图片在 D:\\ws\\生成图片\\gen_y\\image.png',
      streaming: false,
      ts: '',
    }]
    const got = collectAutoPreviewCandidates(blocks, 'D:\\ws')
    expect(got).toEqual([])
  })

  it('drops image_generate result paths even if mentioned only via tool data', () => {
    const blocks: Block[] = [
      imageGenTool('生成图片/gen_z/image.png'),
      writeTool('out/report.md'),
    ]
    const got = collectAutoPreviewCandidates(blocks, '/ws')
    expect(got).toContain('out/report.md')
    expect(got.some((p) => p.includes('生成图片'))).toBe(false)
  })
})
