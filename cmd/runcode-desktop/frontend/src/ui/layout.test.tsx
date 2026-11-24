// @vitest-environment jsdom
import { act, createRef } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it } from 'vitest'
import { PageShell } from './layout'

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
})

function parse(markup: string) {
  const host = document.createElement('div')
  host.innerHTML = markup
  return host.firstElementChild as HTMLElement
}

describe('PageShell', () => {
  it('keepsTheDefaultFrameWithoutOptionalProps', () => {
    const outer = parse(renderToStaticMarkup(<PageShell title="MCP 服务器" hint="说明">正文</PageShell>))
    expect(outer.className).toBe('flex-1 min-h-0 overflow-y-auto overscroll-contain px-[22px] py-7')
    const inner = outer.firstElementChild as HTMLElement
    expect(inner.className).toBe('mx-auto flex flex-col gap-5')
    expect(inner.style.maxWidth).toBe('720px')
    expect(inner.querySelector('h2')?.textContent).toBe('MCP 服务器')
    expect(inner.querySelector('p')?.textContent).toBe('说明')
    expect(inner.textContent).toContain('正文')
  })

  it('appendsContentClassOnlyToThatInstance', () => {
    const markup = renderToStaticMarkup(
      <div>
        <PageShell title="设置" width={864} contentClassName="settings-shell">甲</PageShell>
        <PageShell title="记忆">乙</PageShell>
      </div>,
    )
    const [custom, plain] = Array.from(parse(markup).children) as HTMLElement[]
    const customInner = custom.firstElementChild as HTMLElement
    expect(customInner.className).toBe('mx-auto flex flex-col gap-5 settings-shell')
    expect(customInner.style.maxWidth).toBe('864px')
    expect(custom.className).toBe('flex-1 min-h-0 overflow-y-auto overscroll-contain px-[22px] py-7')
    expect((plain.firstElementChild as HTMLElement).className).toBe('mx-auto flex flex-col gap-5')
    expect(markup.match(/settings-shell/g)).toHaveLength(1)
  })

  it('bindsScrollRefToTheOuterScrollingColumn', async () => {
    const ref = createRef<HTMLDivElement>()
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    await act(async () => root.render(<PageShell title="设置" scrollRef={ref}>正文</PageShell>))
    try {
      expect(ref.current).toBe(host.firstElementChild)
      expect(ref.current?.className).toContain('overflow-y-auto')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
    expect(ref.current).toBeNull()
  })
})
