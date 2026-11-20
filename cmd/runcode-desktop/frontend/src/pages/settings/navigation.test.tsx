// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { PageShell } from '@/ui/layout'
import {
  SETTINGS_CATEGORIES,
  buildSettingsNavigation,
  visibleSectionIds,
  type SettingsCapabilities,
  type SettingsSectionId,
} from './navigation-model'
import { SettingsAnchor, SettingsGroup, SettingsNavigation } from './navigation'
import { useSettingsNavigation } from './use-settings-navigation'

// jsdom has no layout. Geometry is a fixed document: each section's title sits at a
// known offset from the top of the scroll content, the root viewport is 600px tall,
// the content 2000px. getBoundingClientRect derives viewport positions from that and
// the root's scrollTop, which is all the hook reads.
const VIEW_H = 600
const CONTENT_H = 2000
const OFFSETS: Record<SettingsSectionId, number> = {
  passport: 100, login: 260, session: 380, 'custom-models': 560, 'model-context': 760,
  vision: 920, 'image-generation': 1060, recorder: 1200, proxy: 1320, runtimes: 1440,
  'context-audit': 1580, updates: 1700, workspace: 1900,
}

type FakeObserver = { callback: ResizeObserverCallback; targets: Element[]; disconnected: boolean }
let observers: FakeObserver[] = []
let frames: Map<number, FrameRequestCallback>
let nextFrame = 0
let compactHeight = 0
let rootEl: HTMLDivElement | null = null

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  observers = []
  frames = new Map()
  nextFrame = 0
  compactHeight = 56
  rootEl = null
  vi.stubGlobal('ResizeObserver', class {
    private o: FakeObserver
    constructor(callback: ResizeObserverCallback) {
      this.o = { callback, targets: [], disconnected: false }
      observers.push(this.o)
    }
    observe(el: Element) { this.o.targets.push(el) }
    unobserve() {}
    disconnect() { this.o.disconnected = true }
  })
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    nextFrame += 1
    frames.set(nextFrame, cb)
    return nextFrame
  })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => { frames.delete(id) })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const scrollTop = rootEl?.scrollTop ?? 0
    let top = 0
    let height = 0
    if (this.id.startsWith('settings-') && this.id.slice(9) in OFFSETS) {
      top = OFFSETS[this.id.slice(9) as SettingsSectionId] - scrollTop
      height = 100
    } else if (this.classList.contains('settings-compact-nav')) {
      height = compactHeight
    }
    return { top, bottom: top + height, height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON() {} } as DOMRect
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

async function flushFrames() {
  await act(async () => {
    const pending = Array.from(frames.values())
    frames.clear()
    for (const cb of pending) cb(0)
  })
}

async function resize() {
  await act(async () => {
    for (const o of observers) if (!o.disconnected) o.callback([], {} as ResizeObserver)
  })
  await flushFrames()
}

const FULL: SettingsCapabilities = { recorderEnabled: true, auditSupported: true }

function Harness({ caps }: { caps: SettingsCapabilities }) {
  const categories = buildSettingsNavigation(caps)
  const visible = visibleSectionIds(categories)
  const nav = useSettingsNavigation({ visibleIds: visible })
  return (
    <PageShell title="设置" width={864} scrollRef={nav.scrollRef} contentClassName="settings-shell">
      <div ref={nav.layoutRef} className="settings-layout" data-narrow={nav.narrow ? 'true' : 'false'}>
        <SettingsNavigation
          categories={categories}
          activeId={nav.activeId}
          onNavigate={nav.navigateTo}
          narrow={nav.narrow}
          compactNavRef={nav.compactNavRef}
        />
        <div ref={nav.contentRef} className="settings-content">
          {SETTINGS_CATEGORIES.map((c) => (
            <SettingsGroup key={c.id} category={c}>
              {/* Mirrors the page: a brand without the recorder never mounts it, while
                  the audit wrapper stays mounted and is only hidden. */}
              {c.sections.filter((s) => s.id !== 'recorder' || caps.recorderEnabled).map((s) => (
                <SettingsAnchor key={s.id} section={s} register={nav.registerSection} hidden={!visible.includes(s.id)}>
                  <input aria-label={`草稿-${s.id}`} defaultValue="" />
                </SettingsAnchor>
              ))}
            </SettingsGroup>
          ))}
        </div>
      </div>
    </PageShell>
  )
}

type Mounted = {
  host: HTMLDivElement
  root: Root
  scroller: HTMLDivElement
  scrollTo: ReturnType<typeof vi.fn>
  layout: () => HTMLDivElement
  rerender: (caps: SettingsCapabilities) => Promise<void>
  unmount: () => Promise<void>
}

async function mount(caps: SettingsCapabilities = FULL, layoutWidth = 0): Promise<Mounted> {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<Harness caps={caps} />))
  const scroller = host.firstElementChild as HTMLDivElement
  let scrollTop = 0
  Object.defineProperty(scroller, 'clientHeight', { configurable: true, get: () => VIEW_H })
  Object.defineProperty(scroller, 'scrollHeight', { configurable: true, get: () => CONTENT_H })
  Object.defineProperty(scroller, 'scrollTop', { configurable: true, get: () => scrollTop, set: (v: number) => { scrollTop = v } })
  const scrollTo = vi.fn((opts: ScrollToOptions) => { scrollTop = opts.top ?? scrollTop })
  ;(scroller as unknown as { scrollTo: unknown }).scrollTo = scrollTo
  rootEl = scroller
  const layout = () => host.querySelector<HTMLDivElement>('.settings-layout')!
  Object.defineProperty(layout(), 'clientWidth', { configurable: true, get: () => layoutWidth })
  await flushFrames()
  return {
    host, root, scroller, scrollTo, layout,
    rerender: async (next) => { await act(async () => root.render(<Harness caps={next} />)) },
    unmount: async () => { await act(async () => root.unmount()); host.remove() },
  }
}

function setLayoutWidth(m: Mounted, width: number) {
  Object.defineProperty(m.layout(), 'clientWidth', { configurable: true, get: () => width })
}

const current = (host: HTMLElement) =>
  host.querySelector('nav[aria-label="设置目录"] [aria-current="location"]')?.textContent ?? null

function button(host: HTMLElement, text: string) {
  const found = Array.from(host.querySelectorAll('button')).find((b) => b.textContent === text)
  if (!found) throw new Error(`no button ${text}`)
  return found
}

async function click(el: HTMLElement) {
  await act(async () => { el.click() })
}

async function userScroll(m: Mounted, top: number) {
  m.scroller.scrollTop = top
  await act(async () => { m.scroller.dispatchEvent(new Event('scroll')) })
  await flushFrames()
}

// The browser fires the scroll event for a programmatic scrollTo asynchronously.
async function deliverScrollEvent(m: Mounted) {
  await act(async () => { m.scroller.dispatchEvent(new Event('scroll')) })
  await flushFrames()
}

describe('SettingsNavigation (wide)', () => {
  it('rendersFiveCategoriesAndOnlyVisibleSections', async () => {
    const m = await mount({ recorderEnabled: false, auditSupported: false })
    try {
      const nav = m.host.querySelector('nav[aria-label="设置目录"]')!
      const labels = Array.from(nav.querySelectorAll('button')).map((b) => b.textContent)
      expect(labels).toEqual([
        '账号与登录', '账号（通行证）', '登录',
        '模型与对话', '会话', '自定义模型', '模型上下文配置',
        '图片与录音', '图片识别', '图片生成',
        '网络与运行环境', '出网代理', '运行时环境',
        '关于与诊断', '关于与更新', '工作区',
      ])
      expect(current(m.host)).toBe('账号（通行证）')
      expect(nav.querySelectorAll('[aria-current]')).toHaveLength(1)
      expect(m.host.querySelector('#settings-recorder')).toBeNull()
      expect(m.host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(true)
    } finally {
      await m.unmount()
    }
  })

  it('onlyCallsOnNavigateWhenClicked', async () => {
    const onNavigate = vi.fn()
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    await act(async () => root.render(
      <SettingsNavigation categories={buildSettingsNavigation(FULL)} activeId="passport" onNavigate={onNavigate} narrow={false} compactNavRef={{ current: null }} />,
    ))
    await click(button(host, '图片生成'))
    await click(button(host, '关于与诊断'))
    expect(onNavigate.mock.calls).toEqual([['image-generation'], ['context-audit']])
    await act(async () => root.unmount())
  })

  it('scrollsOnlyItselfToRevealTheActiveItem', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    const categories = buildSettingsNavigation(FULL)
    const render = (activeId: SettingsSectionId) => root.render(
      <SettingsNavigation categories={categories} activeId={activeId} onNavigate={() => {}} narrow={false} compactNavRef={{ current: null }} />,
    )
    await act(async () => render('passport'))
    const nav = host.querySelector<HTMLElement>('nav.settings-nav')!
    let navScroll = 0
    Object.defineProperty(nav, 'clientHeight', { configurable: true, get: () => 300 })
    Object.defineProperty(nav, 'scrollHeight', { configurable: true, get: () => 700 })
    Object.defineProperty(nav, 'scrollTop', { configurable: true, get: () => navScroll, set: (v: number) => { navScroll = v } })
    vi.mocked(Element.prototype.getBoundingClientRect).mockImplementation(function (this: Element) {
      const rect = (top: number, height: number) => ({ top, bottom: top + height, height, left: 0, right: 0, width: 0, x: 0, y: top, toJSON() {} }) as DOMRect
      if (this === nav) return rect(0, 300)
      if (this.getAttribute('aria-current') === 'location') return rect(400 - navScroll, 30)
      return rect(0, 0)
    })
    await act(async () => render('workspace'))
    expect(navScroll).toBe(130)
    await act(async () => root.unmount())
  })
})

describe('useSettingsNavigation', () => {
  it('scrollsTheShellRootAndFocusesTheTargetWithoutScrollingIt', async () => {
    const m = await mount()
    try {
      const target = m.host.querySelector<HTMLElement>('#settings-session')!
      const focus = vi.spyOn(target, 'focus')
      await click(button(m.host, '会话'))
      expect(m.scrollTo).toHaveBeenCalledTimes(1)
      expect(m.scrollTo.mock.calls[0][0]).toMatchObject({ top: OFFSETS.session - 24 })
      expect(focus).toHaveBeenCalledWith({ preventScroll: true })
      expect(document.activeElement).toBe(target)
      expect(target.tabIndex).toBe(-1)
      expect(target.getAttribute('aria-label')).toBe('会话')
      expect(current(m.host)).toBe('会话')
    } finally {
      await m.unmount()
    }
  })

  it('navigatesACategoryToItsFirstVisibleSection', async () => {
    const m = await mount({ recorderEnabled: false, auditSupported: false })
    try {
      await click(button(m.host, '关于与诊断'))
      expect(m.scrollTo.mock.calls[0][0]).toMatchObject({ top: Math.min(OFFSETS.updates - 24, CONTENT_H - VIEW_H) })
      expect(current(m.host)).toBe('关于与更新')
    } finally {
      await m.unmount()
    }
  })

  it('keepsAClampedTargetHighlightedUntilTheNextUserScroll', async () => {
    const m = await mount()
    try {
      await click(button(m.host, '关于与更新'))
      expect(m.scrollTo.mock.calls[0][0]).toMatchObject({ top: CONTENT_H - VIEW_H })
      await deliverScrollEvent(m)
      expect(current(m.host)).toBe('关于与更新')
      await resize()
      expect(current(m.host)).toBe('关于与更新')
      await userScroll(m, 1300)
      expect(current(m.host)).toBe('出网代理')
      await userScroll(m, CONTENT_H - VIEW_H)
      expect(current(m.host)).toBe('工作区')
    } finally {
      await m.unmount()
    }
  })

  it('leavesNoLockWhenClickingTheOffsetItIsAlreadyAt', async () => {
    const m = await mount()
    try {
      await click(button(m.host, '关于与更新'))
      await deliverScrollEvent(m)
      await click(button(m.host, '关于与更新'))
      expect(m.scrollTo).toHaveBeenCalledTimes(1)
      expect(current(m.host)).toBe('关于与更新')
      // No scroll event is coming for the second click, so the very next scroll is the user's.
      await userScroll(m, 1300)
      expect(current(m.host)).toBe('出网代理')
    } finally {
      await m.unmount()
    }
  })

  it('swallowsOnlyOneEventForRepeatedClicksBeforeTheScrollLands', async () => {
    const m = await mount()
    try {
      await click(button(m.host, '工作区'))
      await click(button(m.host, '工作区'))
      await deliverScrollEvent(m)
      expect(current(m.host)).toBe('工作区')
      await userScroll(m, 0)
      expect(current(m.host)).toBe('账号（通行证）')
    } finally {
      await m.unmount()
    }
  })

  it('followsUserScrollingWithOneMeasurementPerFrame', async () => {
    const m = await mount()
    try {
      m.scroller.scrollTop = OFFSETS['custom-models'] - 10
      await act(async () => {
        m.scroller.dispatchEvent(new Event('scroll'))
        m.scroller.dispatchEvent(new Event('scroll'))
        m.scroller.dispatchEvent(new Event('scroll'))
      })
      expect(frames.size).toBe(1)
      await flushFrames()
      expect(current(m.host)).toBe('自定义模型')
      await userScroll(m, 0)
      expect(current(m.host)).toBe('账号（通行证）')
      expect(m.scrollTo).not.toHaveBeenCalled()
    } finally {
      await m.unmount()
    }
  })

  it('activatesTheLastSectionWithinTwoPixelsOfTheBottom', async () => {
    const m = await mount()
    try {
      await userScroll(m, CONTENT_H - VIEW_H - 3)
      expect(current(m.host)).toBe('出网代理')
      await userScroll(m, CONTENT_H - VIEW_H - 2)
      expect(current(m.host)).toBe('工作区')
    } finally {
      await m.unmount()
    }
  })

  it('recomputesOnResizeWithoutScrollingOrStealingFocus', async () => {
    const m = await mount()
    try {
      const draft = m.host.querySelector<HTMLInputElement>('[aria-label="草稿-proxy"]')!
      draft.focus()
      m.scroller.scrollTop = OFFSETS.vision
      await resize()
      expect(current(m.host)).toBe('图片识别')
      expect(m.scrollTo).not.toHaveBeenCalled()
      expect(document.activeElement).toBe(draft)
      expect(m.layout().style.getPropertyValue('--settings-root-h')).toBe(`${VIEW_H}px`)
      const targets = observers.flatMap((o) => o.targets)
      expect(targets).toContain(m.scroller)
      expect(targets).toContain(m.layout())
      expect(targets).toContain(m.host.querySelector('.settings-content'))
    } finally {
      await m.unmount()
    }
  })

  it('fallsBackWhenTheActiveSectionIsHidden', async () => {
    const m = await mount()
    try {
      const draft = m.host.querySelector<HTMLInputElement>('[aria-label="草稿-login"]')!
      await click(button(m.host, '上下文审核'))
      draft.focus()
      await m.rerender({ recorderEnabled: true, auditSupported: false })
      expect(current(m.host)).toBe('关于与更新')
      expect(document.activeElement).toBe(draft)
      expect(m.host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(true)
    } finally {
      await m.unmount()
    }
  })

  it('ignoresNavigationToHiddenSections', async () => {
    const m = await mount({ recorderEnabled: true, auditSupported: false })
    try {
      expect(Array.from(m.host.querySelectorAll('nav button')).some((b) => b.textContent === '上下文审核')).toBe(false)
      expect(m.scrollTo).not.toHaveBeenCalled()
    } finally {
      await m.unmount()
    }
  })

  it('keepsDraftNodesAcrossNavigation', async () => {
    const m = await mount()
    try {
      const draft = m.host.querySelector<HTMLInputElement>('[aria-label="草稿-proxy"]')!
      draft.value = '未保存的代理'
      for (let i = 0; i < 3; i++) {
        await click(button(m.host, '账号与登录'))
        await click(button(m.host, '网络与运行环境'))
        await click(button(m.host, '关于与诊断'))
      }
      const after = m.host.querySelector<HTMLInputElement>('[aria-label="草稿-proxy"]')!
      expect(after).toBe(draft)
      expect(after.value).toBe('未保存的代理')
    } finally {
      await m.unmount()
    }
  })

  it('cleansUpListenersObserversAndFramesOnUnmount', async () => {
    const m = await mount()
    m.scroller.scrollTop = 400
    await act(async () => { m.scroller.dispatchEvent(new Event('scroll')) })
    expect(frames.size).toBe(1)
    const scroller = m.scroller
    await m.unmount()
    expect(frames.size).toBe(0)
    expect(observers.every((o) => o.disconnected)).toBe(true)
    scroller.dispatchEvent(new Event('scroll'))
    expect(frames.size).toBe(0)
  })

  it('startsAtTheFirstSectionOnEveryMount', async () => {
    const first = await mount()
    await click(button(first.host, '工作区'))
    await first.unmount()
    const second = await mount()
    try {
      expect(current(second.host)).toBe('账号（通行证）')
      expect(window.location.hash).toBe('')
    } finally {
      await second.unmount()
    }
  })
})

describe('narrow layout', () => {
  async function mountNarrow(width = 863) {
    const m = await mount(FULL, width)
    await resize()
    return m
  }

  it('switchesAt864CssPixelsOfLayoutWidth', async () => {
    const m = await mountNarrow(863)
    try {
      expect(m.layout().dataset.narrow).toBe('true')
      expect(m.host.querySelector('select')).not.toBeNull()
      setLayoutWidth(m, 864)
      await resize()
      expect(m.layout().dataset.narrow).toBe('false')
      expect(m.host.querySelector('select')).toBeNull()
      setLayoutWidth(m, 865)
      await resize()
      expect(m.layout().dataset.narrow).toBe('false')
      setLayoutWidth(m, 863)
      await resize()
      expect(m.layout().dataset.narrow).toBe('true')
    } finally {
      await m.unmount()
    }
  })

  it('keepsDraftsWhenSwitchingLayouts', async () => {
    const m = await mountNarrow(863)
    try {
      const draft = m.host.querySelector<HTMLInputElement>('[aria-label="草稿-session"]')!
      draft.value = '草稿'
      setLayoutWidth(m, 900)
      await resize()
      setLayoutWidth(m, 700)
      await resize()
      expect(m.host.querySelector('[aria-label="草稿-session"]')).toBe(draft)
      expect(draft.value).toBe('草稿')
    } finally {
      await m.unmount()
    }
  })

  it('selectingDoesNotNavigateUntilGo', async () => {
    const m = await mountNarrow()
    try {
      const select = m.host.querySelector('select')!
      expect(select.closest('nav')?.getAttribute('aria-label')).toBe('设置目录')
      expect(Array.from(select.querySelectorAll('optgroup')).map((g) => g.label)).toEqual(['账号与登录', '模型与对话', '图片与录音', '网络与运行环境', '关于与诊断'])
      await act(async () => {
        select.value = 'runtimes'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await act(async () => {
        select.value = 'proxy'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      expect(m.scrollTo).not.toHaveBeenCalled()
      expect(document.activeElement).not.toBe(m.host.querySelector('#settings-proxy'))
      await click(button(m.host, '前往'))
      expect(m.scrollTo).toHaveBeenCalledTimes(1)
      expect(m.scrollTo.mock.calls[0][0]).toMatchObject({ top: OFFSETS.proxy - (compactHeight + 12) })
      expect(document.activeElement).toBe(m.host.querySelector('#settings-proxy'))
      expect(m.host.textContent).toContain('当前：出网代理')
    } finally {
      await m.unmount()
    }
  })

  it('followsTheActiveSectionOnlyWithoutAPendingChoice', async () => {
    const m = await mountNarrow()
    try {
      const select = m.host.querySelector('select')!
      expect(select.value).toBe('passport')
      await userScroll(m, OFFSETS.vision)
      expect(select.value).toBe('vision')
      expect(m.host.textContent).toContain('当前：图片识别')
      await act(async () => {
        select.value = 'login'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await userScroll(m, OFFSETS.proxy)
      expect(select.value).toBe('login')
      expect(m.host.textContent).toContain('当前：出网代理')
      await click(button(m.host, '前往'))
      await deliverScrollEvent(m)
      await userScroll(m, OFFSETS.session)
      expect(select.value).toBe('session')
    } finally {
      await m.unmount()
    }
  })

  it('dropsAPendingChoiceThatBecomesHidden', async () => {
    const m = await mountNarrow()
    try {
      const select = m.host.querySelector('select')!
      await act(async () => {
        select.value = 'context-audit'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await m.rerender({ recorderEnabled: true, auditSupported: false })
      expect(select.value).toBe('passport')
      expect(Array.from(select.options).some((o) => o.value === 'context-audit')).toBe(false)
    } finally {
      await m.unmount()
    }
  })

  it('reMeasuresTheCompactBarHeightForTheOffset', async () => {
    const m = await mountNarrow()
    try {
      compactHeight = 90
      await resize()
      const select = m.host.querySelector('select')!
      await act(async () => {
        select.value = 'runtimes'
        select.dispatchEvent(new Event('change', { bubbles: true }))
      })
      await click(button(m.host, '前往'))
      expect(m.scrollTo.mock.calls[0][0]).toMatchObject({ top: OFFSETS.runtimes - 102 })
      expect(observers.flatMap((o) => o.disconnected ? [] : o.targets)).toContain(m.host.querySelector('.settings-compact-nav'))
    } finally {
      await m.unmount()
    }
  })
})
