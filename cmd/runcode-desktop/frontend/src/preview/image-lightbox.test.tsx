// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ImageLightbox, type ImageLightboxProps } from './image-lightbox'

let host: HTMLDivElement
let root: Root

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div')
  host.id = 'root'
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  document.querySelectorAll('[role="dialog"]').forEach((node) => node.remove())
  vi.restoreAllMocks()
})

async function render(props: Partial<ImageLightboxProps> = {}) {
  const onClose = vi.fn()
  const all: ImageLightboxProps = {
    open: true,
    src: 'blob:preview',
    alt: '生成的图片',
    mode: 'vector',
    onClose,
    ...props,
  }
  await act(async () => root.render(<ImageLightbox {...all} />))
  return { onClose }
}

describe('ImageLightbox', () => {
  it('在客户端内显示模态图片，单击图片关闭', async () => {
    const { onClose } = await render()
    const dialog = document.body.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog).toBeTruthy()
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.dataset.imageLightbox).toBe('vector')
    expect((dialog.querySelector('img') as HTMLImageElement).style.width).toBe('90vw')
    const imageButton = dialog.querySelector('img')?.closest('button') as HTMLButtonElement
    expect(imageButton).toBeTruthy()
    await act(async () => imageButton.click())
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('右上角按钮和 Escape 都能关闭', async () => {
    const first = await render()
    const close = document.body.querySelector('button[aria-label="关闭全屏预览"]') as HTMLButtonElement
    await act(async () => close.click())
    expect(first.onClose).toHaveBeenCalledTimes(1)

    await act(async () => root.unmount())
    root = createRoot(host)
    const second = await render({ src: 'blob:second' })
    await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
    expect(second.onClose).toHaveBeenCalledTimes(1)
  })

  it('位图使用可测试的两倍上限模式，来源变化时关闭', async () => {
    const onClose = vi.fn()
    await render({ mode: 'raster', onClose })
    const dialog = document.body.querySelector('[role="dialog"]') as HTMLElement
    expect(dialog.dataset.imageLightbox).toBe('raster')
    const image = dialog.querySelector('img') as HTMLImageElement
    Object.defineProperties(image, {
      naturalWidth: { configurable: true, value: 400 },
      naturalHeight: { configurable: true, value: 300 },
    })
    await act(async () => image.dispatchEvent(new Event('load')))
    expect(image.style.width).toBe('min(90vw, 800px)')
    expect(image.style.height).toBe('min(90vh, 600px)')
    await act(async () => root.render(
      <ImageLightbox open src="blob:changed" alt="生成的图片" mode="raster" onClose={onClose} />,
    ))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('关闭时恢复来源元素焦点并解除应用根节点 inert', async () => {
    const trigger = document.createElement('button')
    trigger.textContent = '打开'
    document.body.appendChild(trigger)
    trigger.focus()
    const onClose = vi.fn()
    await render({ onClose })
    expect(host.hasAttribute('inert')).toBe(true)

    await act(async () => root.render(
      <ImageLightbox open={false} src="blob:preview" alt="生成的图片" mode="vector" onClose={onClose} />,
    ))
    expect(host.hasAttribute('inert')).toBe(false)
    expect(document.activeElement).toBe(trigger)
    trigger.remove()
  })

  it('单击图片关闭后短时吞掉底层点击', async () => {
    const underlying = vi.fn()
    function Harness() {
      const [open, setOpen] = useState(true)
      return <>
        <button type="button" onClick={underlying}>底层按钮</button>
        <ImageLightbox open={open} src="blob:preview" alt="生成的图片" mode="vector" onClose={() => setOpen(false)} />
      </>
    }
    await act(async () => root.render(<Harness />))
    const imageButton = document.body.querySelector('[role="dialog"] img')?.closest('button') as HTMLButtonElement
    await act(async () => imageButton.click())
    const under = host.querySelector('button') as HTMLButtonElement
    await act(async () => under.click())
    expect(underlying).not.toHaveBeenCalled()
    await new Promise((resolve) => setTimeout(resolve, 280))
    await act(async () => under.click())
    expect(underlying).toHaveBeenCalledTimes(1)
  })
})
