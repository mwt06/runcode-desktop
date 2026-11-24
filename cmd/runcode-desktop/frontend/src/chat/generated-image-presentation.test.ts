import { describe, expect, it } from 'vitest'
import type { ImageGenerationArtifact, ImageGenerationResult, ToolEvent } from '@/core/bridge'
import type { Block } from './blocks'
import {
  buildGeneratedImagePresentation,
  isMergedRasterOpenPreview,
  normalizePresentationPath,
} from './generated-image-presentation'

const png = (path: string, mime = 'image/png'): ImageGenerationArtifact => ({
  path, mimeType: mime, bytes: 100, width: 1024, height: 1024,
})

const result = (path: string, mime = 'image/png', model = 'seedream'): ImageGenerationResult => ({
  version: 1, generationId: 'g1', status: 'succeeded', model, size: '1024x1024',
  accountingStatus: 'unverified', images: [png(path, mime)],
})

const imgTool = (id: string, path: string, sessionId = 'S', mime = 'image/png'): Block => ({
  kind: 'tool', id, sessionId,
  tool: { type: 'completed', toolName: 'image_generate', toolUseID: `tu-${id}`, data: result(path, mime) } as ToolEvent,
})

const previewTool = (id: string, path: string, sessionId = 'S'): Block => ({
  kind: 'tool', id, sessionId,
  tool: {
    type: 'completed', toolName: 'open_preview', toolUseID: `op-${id}`,
    files: [{ path }],
    data: { path },
  } as ToolEvent,
})

describe('normalizePresentationPath', () => {
  it('统一斜杠并去掉 cwd 前缀', () => {
    expect(normalizePresentationPath('生成图片\\g1\\image.png')).toBe('生成图片/g1/image.png')
    expect(normalizePresentationPath('C:/ws/生成图片/g1/image.png', 'C:/ws')).toBe('生成图片/g1/image.png')
  })
})

describe('buildGeneratedImagePresentation', () => {
  it('同会话同完整路径才合并 open_preview，不改原始 blocks', () => {
    const blocks: Block[] = [
      imgTool('a', '生成图片/gen_a/image.png'),
      previewTool('p1', '生成图片/gen_a/image.png'),
      imgTool('b', '生成图片/gen_b/image.png'),
      previewTool('p2', '生成图片/gen_b/image.png'),
    ]
    const frozen = structuredClone(blocks)
    const out = buildGeneratedImagePresentation(blocks, '', {
      sessionId: 'S', sessionEpoch: 1, selectedCopies: new Map(),
    })
    expect(out).toHaveLength(2)
    expect(out[0]!.paths).toEqual(['生成图片/gen_a/image.png'])
    expect(out[0]!.mergedPreviewToolIds).toEqual(['op-p1'])
    expect(out[1]!.mergedPreviewToolIds).toEqual(['op-p2'])
    expect(blocks).toEqual(frozen)
  })

  it('不同 generation 目录同 basename 不合并；跨 session 不合并', () => {
    const blocks: Block[] = [
      imgTool('a', '生成图片/gen_a/image.png'),
      imgTool('b', '生成图片/gen_b/image.png'),
      previewTool('p', '生成图片/gen_b/image.png'),
      { ...previewTool('x', '生成图片/gen_a/image.png', 'OTHER') },
    ]
    const out = buildGeneratedImagePresentation(blocks, '', {
      sessionId: 'S', sessionEpoch: 2, selectedCopies: new Map(),
    })
    expect(out.find((p) => p.ownerBlockId === 'a')!.mergedPreviewToolIds).toEqual([])
    expect(out.find((p) => p.ownerBlockId === 'b')!.mergedPreviewToolIds).toEqual(['op-p'])
  })

  it('选中副本优先进入 paths；JPEG／WebP 栅格同样投影', () => {
    const copy = png('生成图片/gen_a/edited-1.png')
    const blocks: Block[] = [
      imgTool('a', '生成图片/gen_a/image.png', 'S', 'image/jpeg'),
      imgTool('w', '生成图片/gen_w/image.webp', 'S', 'image/webp'),
    ]
    const out = buildGeneratedImagePresentation(blocks, '', {
      sessionId: 'S', sessionEpoch: 1,
      selectedCopies: new Map([['a', copy]]),
    })
    expect(out[0]!.paths[0]).toBe('生成图片/gen_a/edited-1.png')
    expect(out[0]!.paths).toContain('生成图片/gen_a/image.png')
    expect(out).toHaveLength(2)
  })

  it('SVG MIME 不进入栅格投影', () => {
    const blocks: Block[] = [imgTool('s', '生成图片/g/image.svg', 'S', 'image/svg+xml')]
    const out = buildGeneratedImagePresentation(blocks, '', {
      sessionId: 'S', sessionEpoch: 1, selectedCopies: new Map(),
    })
    expect(out).toEqual([])
  })
})

describe('isMergedRasterOpenPreview', () => {
  it('匹配路径的 open_preview 视为已合并', () => {
    const presentations = buildGeneratedImagePresentation(
      [imgTool('a', '生成图片/gen_a/image.png'), previewTool('p1', '生成图片/gen_a/image.png')],
      '',
      { sessionId: 'S', sessionEpoch: 1, selectedCopies: new Map() },
    )
    const merged = presentations[0]!.mergedPreviewToolIds[0]!
    const tool = { toolName: 'open_preview', toolUseID: merged, files: [{ path: '生成图片/gen_a/image.png' }] } as ToolEvent
    expect(isMergedRasterOpenPreview(tool, presentations, '', 'S')).toBe(true)
    expect(isMergedRasterOpenPreview(
      { toolName: 'open_preview', files: [{ path: 'docs/a.md' }] } as ToolEvent,
      presentations, '', 'S',
    )).toBe(false)
  })
})
