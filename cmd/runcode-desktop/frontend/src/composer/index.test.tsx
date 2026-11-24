// @vitest-environment jsdom
import { act, createRef } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CONTROLLED_TEXT_INSERTION_COMMAND, getNearestEditorFromDOMNode } from 'lexical'
import type { SessionInfo } from '@/core/bridge'
import type { Attachment } from './paste'
import { Composer } from './index'
import type { ComposerEditorHandle } from './inline-editor'

const directoryCommand = vi.hoisted(() => vi.fn())
const imageApi = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }))

vi.mock('@/core/bridge', () => ({
  listAgents: async () => ({ agents: [] }),
  listSkills: async () => ({ skills: [] }),
  listCustomModels: async () => [],
  sessionModels: async () => [],
  pickImageAttachment: async () => '',
  savePastedFile: async () => '',
  errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  getSessionImageGeneration: imageApi.get,
  setSessionImageGeneration: imageApi.set,
  onEvent: () => () => {},
  Events: { PassportChanged: 'passport:changed' },
}))

const imageRef = { kind: 'platform', name: 'image-model', bridge: 'https://bridge.example', tenantId: 't1' }
function imageView(sessionId: string, patch: Record<string, unknown> = {}) {
  return { sessionId, enabled: false, revision: 0, available: true, model: imageRef, size: '1024x1024', ...patch }
}

const noop = () => {}
let host: HTMLDivElement, root: Root

beforeEach(() => {
  directoryCommand.mockReset()
  imageApi.get.mockReset().mockImplementation(async (id: string) => imageView(id))
  imageApi.set.mockReset().mockImplementation(async (req: { sessionId: string; enabled: boolean; expectedRevision: number }) => imageView(req.sessionId, { enabled: req.enabled, revision: req.expectedRevision + 1 }))
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('IntersectionObserver', class { observe() {} unobserve() {} disconnect() {} })
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: noop })
  Range.prototype.getBoundingClientRect = () => new DOMRect()
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Selection.prototype.modify = function (_alter, direction) {
    const walker = document.createTreeWalker(host.querySelector('[role="textbox"]')!, NodeFilter.SHOW_TEXT)
    const texts: Text[] = []
    while (walker.nextNode()) texts.push(walker.currentNode as Text)
    let index = texts.indexOf(this.focusNode as Text), offset = this.focusOffset
    if (index < 0) return
    if (direction === 'backward') {
      if (offset > 0) offset--
      else if (index > 0) { index--; offset = texts[index].length - 1 }
    } else {
      if (offset < texts[index].length) offset++
      else if (index < texts.length - 1) { index++; offset = 1 }
    }
    this.collapse(texts[index], offset)
  }
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function mount(options: { busy?: boolean; attachments?: Attachment[]; sendBlock?: string; sessionId?: string } = {}) {
  const handle = createRef<ComposerEditorHandle>(), chat = createRef<HTMLDivElement>()
  let text = 'abcd'
  let toast = ''
  let sessionId = options.sessionId ?? 'A'
  const saves: string[][] = []
  const sends: string[] = []
  const refreshFiles = vi.fn()
  const render = () => {
    root.render(<main><div ref={chat} /><Composer attachments={options.attachments ?? []} setAttachments={noop}
      retry={undefined} retryDisabled={false} submitting={false} onCancelRetry={noop} onRemoveOriginal={noop}
      input={text} onInputChange={(value) => { text = value; render() }} taRef={handle} chatScrollRef={chat}
      busy={options.busy ?? false} toast={toast} onNotify={(message) => { toast = message; render() }} info={options.sessionId ? { sessionId: options.sessionId } as SessionInfo : null} files={['name']} sessionId={sessionId} onRefreshFiles={refreshFiles}
      onSend={async body => { sends.push(body); return true }} onStop={noop} onToggleMode={noop} onTogglePlan={noop}
      onChooseReasoning={noop} onChooseThinking={noop} onPickModel={noop} onPickScenario={noop} /></main>)
  }
  await act(async () => render())
  const textbox = host.querySelector<HTMLElement>('[role="textbox"]')!
  const editor = getNearestEditorFromDOMNode(textbox)!
  const switchSession = async (id: string) => { sessionId = id; await act(async () => render()) }
  return { handle, textbox, editor, text: () => text, saves, sends, refreshFiles, toast: () => toast, switchSession }
}

const findButton = (label: string) => Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.textContent?.trim().startsWith(label))
async function openAddMenu() {
  await act(async () => host.querySelector<HTMLButtonElement>('button[title^="添加："]')!.click())
}

async function nextFrame() {
  await act(async () => { await new Promise<void>((resolve) => requestAnimationFrame(() => resolve())) })
}

describe('Composer inline editor integration', () => {

  it('keepsFileAndAgentActionsAvailableWithSelection', async () => {
    const app = await mount({ busy: false })
    await act(async () => host.querySelector<HTMLButtonElement>('button[title^="添加："]')!.click())
    const file = Array.from(host.querySelectorAll('button')).find(button => button.textContent?.trim() === '文件')!
    expect(file.disabled).toBe(false)
    expect(app.text()).toBe('abcd')
  })


  it('moves a middle caret to the appended file trigger before typing its name', async () => {
    const app = await mount()
    await act(async () => app.handle.current!.setSelectionRange(2, 2))
    expect(app.handle.current!.getSelection()).toEqual({ start: 2, end: 2 })
    await act(async () => { host.querySelector<HTMLButtonElement>('button[title^="添加："]')!.click() })
    const fileOption = Array.from(host.querySelectorAll<HTMLButtonElement>('button')).find((element) => element.textContent?.trim() === '文件')!
    await act(async () => { fileOption.click() })
    await nextFrame()
    expect(app.text()).toBe('abcd #')
    await act(async () => { app.editor.dispatchCommand(CONTROLLED_TEXT_INSERTION_COMMAND, 'name') })
    expect(app.text()).toBe('abcd #name')
    expect(app.refreshFiles).toHaveBeenCalledOnce()
  })
})

describe('Composer image generation entry', () => {
  it('「+」菜单与工具条不再暴露生成图片开关或生图芯片', async () => {
    const app = await mount()
    await openAddMenu()
    expect(findButton('生成图片')).toBeUndefined()
    expect(host.querySelector('button[title^="添加："]')!.getAttribute('title')).toBe('添加：技能 / 智能体 / 文件 / 图片')
    expect(host.textContent).not.toContain('关闭生图')
    expect(findButton('生图')).toBeUndefined()
    expect(imageApi.set).not.toHaveBeenCalled()
    await act(async () => app.textbox.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })))
    expect(app.sends).toEqual(['abcd'])
    expect(imageApi.set).not.toHaveBeenCalled()
  })
})
