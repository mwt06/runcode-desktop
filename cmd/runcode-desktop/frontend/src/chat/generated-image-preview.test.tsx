// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ImageGenerationArtifact, ToolEvent } from '@/core/bridge'

const api = vi.hoisted(() => ({ openArtifact: vi.fn(), releaseArtifact: vi.fn() }))
vi.mock('@/core/bridge', async () => {
  const actual = await vi.importActual<typeof import('@/core/bridge')>('@/core/bridge')
  return {
    ...actual,
    ...api,
    errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    errCode: (e: unknown) => { try { return JSON.parse((e as Error).message).code ?? '' } catch { return '' } },
  }
})

import { GeneratedImagePreview } from './generated-image-preview'

const image: ImageGenerationArtifact = {
  path: '生成图片/gen_1/image.png', mimeType: 'image/png', bytes: 2048, width: 1024, height: 1024,
}
const tool = (type: ToolEvent['type'] = 'completed', extra: Partial<ToolEvent> = {}): ToolEvent => ({
  type, toolName: 'image_generate', toolUseID: 'tu_1', ...extra,
} as ToolEvent)

let host: HTMLDivElement, root: Root
beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  api.openArtifact.mockReset().mockResolvedValue({
    reference: 'ref-1', sessionId: 'S', generation: 'g', path: image.path, url: 'http://127.0.0.1:9/ref-1',
  })
  api.releaseArtifact.mockReset().mockResolvedValue(undefined)
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private cb: IntersectionObserverCallback) {}
    observe(el: Element) { this.cb([{ isIntersecting: true, target: el } as IntersectionObserverEntry], this as unknown as IntersectionObserver) }
    disconnect() {}
    unobserve() {}
  })
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.unstubAllGlobals()
})

describe('GeneratedImagePreview', () => {
  it('生成中只有下方扫光，无卡片缩略图 class', async () => {
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('progress')} sessionId="S" />,
    ))
    expect(host.querySelector('.ig-preview-shimmer')).toBeTruthy()
    expect(host.textContent).toContain('预览图片')
  })

  it('点击「预览 …」标题可折叠／展开舞台', async () => {
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('progress')} sessionId="S" />,
    ))
    const title = host.querySelector('button[aria-expanded="true"]')
    expect(title).toBeTruthy()
    expect(host.querySelector('.ig-preview-shimmer')).toBeTruthy()
    await act(async () => { title!.dispatchEvent(new MouseEvent('click', { bubbles: true })) })
    expect(host.querySelector('button[aria-expanded="false"]')).toBeTruthy()
    expect(host.querySelector('.ig-preview-shimmer')).toBeNull()
    await act(async () => {
      host.querySelector('button[aria-expanded="false"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(host.querySelector('.ig-preview-shimmer')).toBeTruthy()
  })

  it('ready 图 decode 后才可交互；历史直挂不播揭示；角标仅图标', async () => {
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('completed')} sessionId="S" image={image} />,
    ))
    await act(async () => { await Promise.resolve() })
    const img = host.querySelector('img')
    expect(img).toBeTruthy()
    expect(host.querySelector('.ig-preview-reveal')).toBeNull()
    expect(host.querySelectorAll('.ig-preview-corner-btn')).toHaveLength(0)
    await act(async () => {
      img!.dispatchEvent(new Event('load'))
      await Promise.resolve()
    })
    expect(host.querySelector('.ig-preview-reveal')).toBeNull()
    const corners = [...host.querySelectorAll('.ig-preview-corner-btn')]
    // 标注角标随 IMAGE_GENERATION_ANNOTATION_UI 下线；恢复开关后应再含「标注」。
    expect(corners.map((el) => el.getAttribute('aria-label'))).toEqual([
      '查看大图', '在文件夹中显示', '用系统程序打开',
    ])
  })

  it('running→ready 且 decode 后播放揭示；animationend 清除', async () => {
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('progress')} sessionId="S" />,
    ))
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('completed')} sessionId="S" image={image} />,
    ))
    await act(async () => { await Promise.resolve() })
    const img = host.querySelector('img')!
    await act(async () => {
      img.dispatchEvent(new Event('load'))
      await Promise.resolve()
    })
    await act(async () => { await Promise.resolve() })
    const reveal = host.querySelector('.ig-preview-reveal')
    expect(reveal).toBeTruthy()
    const last = reveal!.querySelector('span:last-child')!
    await act(async () => { last.dispatchEvent(new Event('animationend', { bubbles: true })) })
    expect(host.querySelector('.ig-preview-reveal')).toBeNull()
  })

  it('生成中到完成保持预览舞台尺寸，不因真实宽高变化而推动对话', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(560)
    try {
      await act(async () => root.render(
        <GeneratedImagePreview tool={tool('progress')} sessionId="S" />,
      ))
      const placeholder = host.querySelector('.ig-preview-shimmer') as HTMLElement
      const before = { width: placeholder.style.width, height: placeholder.style.height }

      await act(async () => root.render(
        <GeneratedImagePreview
          tool={tool('completed')}
          sessionId="S"
          image={{ ...image, width: 1280, height: 720 }}
        />,
      ))
      await act(async () => { await Promise.resolve() })
      const stage = host.querySelector('.ig-preview-stage') as HTMLElement
      expect({ width: stage.style.width, height: stage.style.height }).toEqual(before)
    } finally {
      width.mockRestore()
    }
  })

  it('not_found 显示文件不存在；其它错误显示暂时无法预览', async () => {
    api.openArtifact.mockRejectedValueOnce(new Error(JSON.stringify({ code: 'not_found', message: 'gone' })))
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('completed')} sessionId="S" image={image} />,
    ))
    await act(async () => { await Promise.resolve() })
    expect(host.textContent).toContain('文件不存在')
    expect(host.textContent).not.toContain('gone')

    api.openArtifact.mockRejectedValueOnce(new Error(JSON.stringify({ code: 'permission_denied', message: 'nope' })))
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('completed')} sessionId="S" image={{ ...image, path: '生成图片/gen_2/image.png' }} />,
    ))
    await act(async () => { await Promise.resolve() })
    expect(host.textContent).toContain('暂时无法预览')
    expect(host.textContent).toContain('重新加载预览')
  })

  it('卸载释放引用', async () => {
    await act(async () => root.render(
      <GeneratedImagePreview tool={tool('completed')} sessionId="S" image={image} />,
    ))
    await act(async () => { await Promise.resolve() })
    await act(async () => root.unmount())
    root = createRoot(host)
    expect(api.releaseArtifact).toHaveBeenCalledWith('ref-1')
  })
})
