// @vitest-environment jsdom
import { act, createElement, useRef, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  HtmlPreviewBudgetStore, InlinePreviewBudgetProvider, useHtmlPreviewSlot,
} from './inline-preview-budget'

type IoEntry = { cb: IntersectionObserverCallback; el: Element | null }

let host: HTMLDivElement
let root: Root
let observers: IoEntry[]

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  observers = []
  vi.stubGlobal('IntersectionObserver', class {
    constructor(private cb: IntersectionObserverCallback) {
      observers.push({ cb, el: null })
    }
    observe(el: Element) {
      const last = observers[observers.length - 1]
      if (last) last.el = el
    }
    unobserve() {}
    disconnect() {}
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

function fireNear(index: number, near: boolean) {
  const o = observers[index]
  if (!o?.el) throw new Error(`observer ${index} missing`)
  o.cb([{ isIntersecting: near, target: o.el } as IntersectionObserverEntry], o as unknown as IntersectionObserver)
}

function SlotProbe({ enabled = true, label }: { enabled?: boolean; label: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const has = useHtmlPreviewSlot(ref, enabled)
  return createElement('div', { ref, 'data-label': label, 'data-slot': has ? '1' : '0' })
}

function mount(store: HtmlPreviewBudgetStore, labels: string[]) {
  return act(async () => {
    root.render(createElement(InlinePreviewBudgetProvider, {
      store,
      limit: 4,
      children: labels.map((label) => createElement(SlotProbe, { key: label, label })),
    }))
  })
}

describe('HtmlPreviewBudgetStore / useHtmlPreviewSlot', () => {
  it('同消息六块：活动名额最多 4；离屏释放后后续可获取', async () => {
    const store = new HtmlPreviewBudgetStore(4)
    await mount(store, ['a', 'b', 'c', 'd', 'e', 'f'])
    expect(observers).toHaveLength(6)
    for (let i = 0; i < 6; i++) await act(async () => fireNear(i, true))
    expect(store.holderCount()).toBe(4)
    const slotted = [...host.querySelectorAll('[data-slot="1"]')].map((el) => el.getAttribute('data-label'))
    expect(slotted).toHaveLength(4)
    expect(slotted).toEqual(['a', 'b', 'c', 'd'])

    await act(async () => fireNear(0, false))
    await act(async () => fireNear(1, false))
    expect([...host.querySelectorAll('[data-slot="1"]')].map((el) => el.getAttribute('data-label')).sort())
      .toEqual(['c', 'd', 'e', 'f'].sort())
  })

  it('跨多个消息的 Provider 仍共享同一上限 4', async () => {
    const store = new HtmlPreviewBudgetStore(4)
    function Multi() {
      const [n] = useState(5)
      return createElement(InlinePreviewBudgetProvider, {
        store,
        children: [
          createElement('div', { key: '1', 'data-msg': '1' },
            createElement(SlotProbe, { label: 'm1-a' }),
            createElement(SlotProbe, { label: 'm1-b' }),
            createElement(SlotProbe, { label: 'm1-c' }),
          ),
          createElement('div', { key: '2', 'data-msg': '2' },
            ...Array.from({ length: n }, (_, i) => createElement(SlotProbe, { key: i, label: `m2-${i}` })),
          ),
        ],
      })
    }
    await act(async () => root.render(createElement(Multi)))
    for (let i = 0; i < observers.length; i++) await act(async () => fireNear(i, true))
    expect(store.holderCount()).toBe(4)
    expect(host.querySelectorAll('[data-slot="1"]')).toHaveLength(4)
  })

  it('无 IntersectionObserver 时最多挂前 4 个，其余不得占名额', async () => {
    vi.stubGlobal('IntersectionObserver', undefined)
    const store = new HtmlPreviewBudgetStore(4)
    await act(async () => {
      root.render(createElement(InlinePreviewBudgetProvider, {
        store,
        children: ['a', 'b', 'c', 'd', 'e', 'f'].map((label) => createElement(SlotProbe, { key: label, label })),
      }))
    })
    expect(store.holderCount()).toBe(4)
    const slotted = [...host.querySelectorAll('[data-slot="1"]')].map((el) => el.getAttribute('data-label'))
    expect(slotted).toEqual(['a', 'b', 'c', 'd'])
    expect([...host.querySelectorAll('[data-slot="0"]')].map((el) => el.getAttribute('data-label')))
      .toEqual(['e', 'f'])
  })
})
