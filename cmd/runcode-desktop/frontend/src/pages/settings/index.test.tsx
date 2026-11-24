// @vitest-environment jsdom
import { act, type ReactElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionInfo } from '@/core/bridge'

const api = vi.hoisted(() => ({
  saveSettings: vi.fn(),
  listCustomModels: vi.fn(),
  contextAuditStatus: vi.fn(),
  setContextAudit: vi.fn(),
  webProxy: vi.fn(),
  setWebProxy: vi.fn(),
  passportStatus: vi.fn(),
  passportTenants: vi.fn(),
  passportModels: vi.fn(),
  activeTenant: vi.fn(),
  setActiveTenant: vi.fn(),
  secretStorageStatus: vi.fn(),
  getVisionSettings: vi.fn(),
  saveVisionSettings: vi.fn(),
  listImageGenerationModels: vi.fn(),
  getImageGenerationSettings: vi.fn(),
  saveImageGenerationSettings: vi.fn(),
  getModelContextSettings: vi.fn(),
  saveModelContextSettings: vi.fn(),
  recorderSettings: vi.fn(),
  recorderDevices: vi.fn(),
  saveRecorderSettings: vi.fn(),
  runtimeStatus: vi.fn(),
  checkRuntimes: vi.fn(),
  installRuntime: vi.fn(),
  authorizeRuntime: vi.fn(),
  cancelRuntimeInstall: vi.fn(),
  removeRuntime: vi.fn(),
  getMailSettings: vi.fn(),
  saveMailAccount: vi.fn(),
  testMailConnection: vi.fn(),
  sendMailTestToSelf: vi.fn(),
  confirmMailTestReceipt: vi.fn(),
  openMailOfficialURL: vi.fn(),
  onEvent: vi.fn(() => () => {}),
}))

const brand = vi.hoisted(() => ({
  key: 'runcode',
  name: 'XRUN',
  tagline: '',
  loginHeadline: '',
  greeting: 'explore' as const,
  logo: { kind: 'mark' as const },
  features: { recorder: true },
  scenarioProfile: 'full' as const,
}))

vi.mock('@/core/brand', () => ({ BRAND: brand, selectBrand: () => brand }))
vi.mock('@/core/bridge', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/core/bridge')>()
  return {
    ...actual,
    ...api,
    errText: (e: unknown) => (e instanceof Error ? e.message : String(e)),
  }
})

import { SettingsPage } from './index'
import { SettingsSaveProvider } from './settings-save-provider'

const custom = { name: 'local-llm', provider: 'openai-compatible', model: 'qwen', baseURL: 'http://127.0.0.1:11434', hasAPIKey: false }
const contextView = {
  model: { kind: 'custom', name: 'local-llm' },
  defaults: { maxOutputTokens: 8192, maxContextTokens: 128000, maxHistoryMessages: 0 },
  overrides: {},
  effective: { maxOutputTokens: 8192, maxContextTokens: 128000, maxHistoryMessages: 0 },
}

const update = {
  info: null, stage: 'idle' as const, hasUpdate: false, busy: false, error: '',
  check: vi.fn(), download: vi.fn(), cancel: vi.fn(), install: vi.fn(), reveal: vi.fn(),
}
const onSwitchModel = vi.fn()
const onSaved = vi.fn()

function info(patch: Partial<SessionInfo> = {}): SessionInfo {
  return {
    sessionId: 'A', model: 'platform-model', permissionMode: 'interactive',
    planMode: false, thinkingEffort: 'medium', reasoningScenario: 'auto', cwd: 'C:\\ws',
    ...patch,
  } as SessionInfo
}

let host: HTMLDivElement
let root: Root
let frames: Map<number, FrameRequestCallback>
let nextFrame = 0

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  brand.features.recorder = true
  frames = new Map()
  nextFrame = 0
  for (const fn of Object.values(api)) fn.mockReset()
  api.onEvent.mockReturnValue(() => {})
  api.saveSettings.mockResolvedValue(info())
  api.listCustomModels.mockResolvedValue([custom])
  api.contextAuditStatus.mockResolvedValue({ supported: false, enabled: false, url: '', dir: '' })
  api.webProxy.mockResolvedValue('')
  api.setWebProxy.mockImplementation(async (v: string) => v)
  api.passportStatus.mockResolvedValue({ loggedIn: false })
  api.passportTenants.mockResolvedValue([])
  api.passportModels.mockResolvedValue([])
  api.activeTenant.mockResolvedValue('')
  api.secretStorageStatus.mockResolvedValue({ ok: true, reason: '', fix: '' })
  api.getVisionSettings.mockResolvedValue({ platformCapabilities: [] })
  api.listImageGenerationModels.mockResolvedValue({ scopeId: 's', models: [] })
  api.getImageGenerationSettings.mockResolvedValue({ scopeId: 's', revision: 0, defaultSize: '' })
  api.getModelContextSettings.mockResolvedValue(contextView)
  api.recorderSettings.mockResolvedValue({
    gatewayUrl: '', speakerName: '', lang: '', version: 0, micDeviceId: '', sysDeviceId: '',
    keepAudio: true, summaryModel: '', gatewayToken: '', micDiarize: false, autoFullMinutes: false,
  })
  api.recorderDevices.mockResolvedValue({ mics: [], systems: [] })
  api.runtimeStatus.mockResolvedValue({ platform: 'windows/amd64', packs: [], fetched: true, error: '' })
  api.getMailSettings.mockResolvedValue({ secretStorageOk: true })
  api.openMailOfficialURL.mockResolvedValue(undefined)
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { nextFrame += 1; frames.set(nextFrame, cb); return nextFrame })
  vi.stubGlobal('cancelAnimationFrame', (id: number) => { frames.delete(id) })
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
})

afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

async function flush() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
  await act(async () => {
    const pending = Array.from(frames.values())
    frames.clear()
    for (const cb of pending) cb(0)
  })
}

async function mount(page: ReactElement) {
  await act(async () => root.render(page))
  await flush()
}

function page(opts: { busy?: boolean; session?: SessionInfo; initial?: Record<string, unknown>; initialSection?: 'image-generation' | 'passport' } = {}) {
  return (
    <SettingsSaveProvider>
      <SettingsPage
        initial={{ permissionMode: 'interactive', skipLogin: false, harmJudgeModel: '', harmJudgeVotes: 1, ...(opts.initial ?? {}) }}
        info={opts.session ?? info()}
        busy={opts.busy ?? false}
        update={update}
        initialSection={opts.initialSection}
        onSaved={onSaved}
        onSwitchModel={onSwitchModel}
      />
    </SettingsSaveProvider>
  )
}

function navButton(text: string) {
  const found = Array.from(host.querySelectorAll('nav[aria-label="设置目录"] button')).find((b) => b.textContent === text)
  if (!found) throw new Error(`nav button missing: ${text}`)
  return found as HTMLButtonElement
}

async function go(text: string) {
  await act(async () => { navButton(text).click() })
  await flush()
}

function skipToggle() {
  const label = Array.from(host.querySelectorAll('label')).find((el) => el.textContent?.includes('免登录'))
  const toggle = label?.querySelector<HTMLButtonElement>('[role="switch"]')
  if (!toggle) throw new Error('skipLogin toggle missing')
  return toggle
}

function permissionSelect() {
  const label = Array.from(host.querySelectorAll('label')).find((el) => el.textContent?.startsWith('权限模式'))
  const select = label?.querySelector('select')
  if (!select) throw new Error('permission select missing')
  return select
}

function votesSelect() {
  const label = Array.from(host.querySelectorAll('label')).find((el) => el.textContent?.includes('判定表决'))
  const select = label?.querySelector('select')
  if (!select) throw new Error('votes select missing')
  return select
}

function proxyInput() {
  return host.querySelector<HTMLInputElement>('#settings-proxy input')!
}

function customNameInput() {
  return host.querySelector<HTMLInputElement>('#settings-custom-models input[placeholder="显示名称（如 本地 Ollama）"]')!
}

async function typeInto(el: HTMLInputElement, value: string) {
  await act(async () => {
    el.focus()
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    setter.call(el, value)
    el.dispatchEvent(new InputEvent('input', { bubbles: true, cancelable: true, data: value, inputType: 'insertText' }))
  })
}

describe('SettingsPage navigation shell', () => {
  it('showsOnlyTheSelectedCategoryContentWhileKeepingAllDirectoryItemsVisible', async () => {
    await mount(page())
    expect(host.querySelector('input[placeholder="搜索设置"]')).not.toBeNull()
    expect(navButton('账号与登录').hasAttribute('aria-expanded')).toBe(false)
    expect(navButton('模型与对话').hasAttribute('aria-expanded')).toBe(false)
    expect(host.querySelector('#settings-category-account-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('#settings-category-models-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('.settings-page-heading')?.textContent).toContain('账号与登录')
    expect(host.querySelector<HTMLElement>('#settings-passport')!.closest<HTMLElement>('.settings-group')!.hidden).toBe(false)
    expect(host.querySelector<HTMLElement>('#settings-login')!.closest<HTMLElement>('.settings-group')!.hidden).toBe(false)
    expect(host.querySelector<HTMLElement>('#settings-session')!.closest<HTMLElement>('.settings-group')!.hidden).toBe(true)
  })

  it('navigatesFromTheSettingsSearchResult', async () => {
    await mount(page())
    const input = host.querySelector<HTMLInputElement>('input[placeholder="搜索设置"]')!
    await act(async () => {
      input.focus()
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, '图片生成')
      input.dispatchEvent(new InputEvent('input', { bubbles: true, data: '图片生成', inputType: 'insertText' }))
    })
    const result = Array.from(host.querySelectorAll<HTMLButtonElement>('[role="option"]')).find((node) => node.textContent?.includes('图片生成'))
    if (!result) throw new Error('search result missing')
    await act(async () => result.click())
    await flush()
    expect(host.querySelector('#settings-category-media-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('.settings-page-heading')?.textContent).toContain('图片与录音')
    expect(host.querySelector('nav [aria-current="location"]')?.textContent).toBe('图片生成')
  })

  it('showsTheFullClickedCategoryAndSelectsItsFirstItem', async () => {
    await mount(page())
    await go('模型与对话')
    expect(navButton('账号与登录').hasAttribute('aria-expanded')).toBe(false)
    expect(navButton('模型与对话').hasAttribute('aria-expanded')).toBe(false)
    expect(host.querySelector('#settings-category-account-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('#settings-category-models-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('.settings-page-heading')?.textContent).toContain('模型与对话')
    expect(host.querySelector('nav [aria-current="location"]')?.textContent).toBe('会话')
    const modelGroup = host.querySelector<HTMLElement>('#settings-session')!.closest<HTMLElement>('.settings-group')!
    expect(modelGroup.hidden).toBe(false)
    expect(modelGroup.querySelectorAll('section[id^="settings-"]')).toHaveLength(3)
  })

  it('keepsTheCurrentCategoryOpenWhenItsHeadingIsClickedAgain', async () => {
    await mount(page())
    await go('模型与对话')
    await go('模型与对话')
    expect(navButton('模型与对话').hasAttribute('aria-expanded')).toBe(false)
    expect(navButton('模型与对话').classList.contains('settings-nav-category-active')).toBe(true)
    expect(host.querySelector('#settings-category-models-sections')?.hasAttribute('hidden')).toBe(false)
    expect(host.querySelector('.settings-page-heading')?.textContent).toContain('模型与对话')
    expect(host.querySelector<HTMLElement>('#settings-session')!.closest<HTMLElement>('.settings-group')!.hidden).toBe(false)
  })

  it('keepsBothAccountCardsVisibleWhenLoginIsSelected', async () => {
    await mount(page())
    await go('登录')
    const accountGroup = host.querySelector<HTMLElement>('#settings-passport')!.closest<HTMLElement>('.settings-group')!
    expect(accountGroup.hidden).toBe(false)
    expect(accountGroup.contains(host.querySelector('#settings-login'))).toBe(true)
    expect(host.querySelector<HTMLElement>('#settings-session')!.closest<HTMLElement>('.settings-group')!.hidden).toBe(true)
    expect(host.querySelector('nav [aria-current="location"]')?.textContent).toBe('登录')
  })

  it('rendersDirectoryAndAnchorsInCatalogOrderIncludingImageGeneration', async () => {
    await mount(page())
    const labels = Array.from(host.querySelectorAll('nav[aria-label="设置目录"] button')).map((b) => b.textContent)
    expect(labels).toContain('图片生成')
    expect(labels.indexOf('图片识别')).toBeLessThan(labels.indexOf('图片生成'))
    expect(labels.indexOf('图片生成')).toBeLessThan(labels.indexOf('录音纪要'))
    expect(host.querySelector('#settings-image-generation')).not.toBeNull()
    expect(host.querySelector('#settings-image-generation')!.textContent).toContain('图片生成')
    expect(host.querySelector('.settings-layout')).not.toBeNull()
    expect(host.querySelector('.settings-page-heading')?.textContent).toContain('账号与登录')
    expect(host.textContent).not.toContain('保存设置')
    const anchors = Array.from(host.querySelectorAll('section[id^="settings-"]')).map((el) => el.id)
    expect(anchors).toEqual([
      'settings-passport', 'settings-login', 'settings-mail',
      'settings-session', 'settings-custom-models', 'settings-model-context',
      'settings-vision', 'settings-image-generation', 'settings-recorder',
      'settings-proxy', 'settings-runtimes',
      'settings-context-audit', 'settings-updates', 'settings-workspace',
    ])
    expect(host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(true)
    expect(labels).not.toContain('上下文审核')
  })

  it('initialSectionFocusesImageGenerationInDirectory', async () => {
    await mount(page({ initialSection: 'image-generation' }))
    const active = host.querySelector('nav[aria-label="设置目录"] button[aria-current="location"]')
    expect(active?.textContent).toBe('图片生成')
    expect(navButton('图片与录音').hasAttribute('aria-expanded')).toBe(false)
    expect(host.querySelector('#settings-category-media-sections')?.hasAttribute('hidden')).toBe(false)
    expect(document.activeElement?.closest('#settings-image-generation')).not.toBeNull()
  })

  it('showsAuditDirectoryOnlyAfterSupportedCallback', async () => {
    let release!: (info: { supported: boolean; enabled: boolean; url: string; dir: string }) => void
    api.contextAuditStatus.mockReturnValue(new Promise((r) => { release = r }))
    await mount(page())
    expect(Array.from(host.querySelectorAll('nav button')).some((b) => b.textContent === '上下文审核')).toBe(false)
    expect(host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(true)
    await act(async () => { release({ supported: true, enabled: false, url: 'http://127.0.0.1:9', dir: 'D:\\audit' }) })
    await flush()
    expect(Array.from(host.querySelectorAll('nav button')).some((b) => b.textContent === '上下文审核')).toBe(true)
    expect(host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(false)
    expect(host.textContent).toContain('开启上下文审核')
    expect(api.contextAuditStatus).toHaveBeenCalledTimes(1)
  })

  it('hidesAuditOnReadFailureAndDoesNotRemountProbe', async () => {
    api.contextAuditStatus.mockRejectedValueOnce(new Error('boom'))
    await mount(page())
    expect(host.querySelector('#settings-context-audit')).not.toBeNull()
    expect(host.querySelector<HTMLElement>('#settings-context-audit')!.hidden).toBe(true)
    expect(api.contextAuditStatus).toHaveBeenCalledTimes(1)
    await go('关于与更新')
    await go('账号（通行证）')
    expect(api.contextAuditStatus).toHaveBeenCalledTimes(1)
  })

  it('omitsRecorderWhenBrandDisablesIt', async () => {
    brand.features.recorder = false
    await mount(page())
    expect(host.querySelector('#settings-recorder')).toBeNull()
    expect(Array.from(host.querySelectorAll('nav button')).some((b) => b.textContent === '录音纪要')).toBe(false)
    expect(host.querySelector('#settings-image-generation')).not.toBeNull()
  })
})

describe('SettingsPage draft and save contracts', () => {
  it('autosavesTheLatestFourFieldsAcrossCategoryTrips', async () => {
    await mount(page())
    await act(async () => { skipToggle().click() })
    await act(async () => {
      permissionSelect().value = 'judge'
      permissionSelect().dispatchEvent(new Event('change', { bubbles: true }))
      votesSelect().value = '3'
      votesSelect().dispatchEvent(new Event('change', { bubbles: true }))
    })
    await typeInto(customNameInput(), '草稿模型')
    expect(customNameInput().value).toBe('草稿模型')
    await typeInto(proxyInput(), '127.0.0.1:7890')
    expect(proxyInput().value).toBe('127.0.0.1:7890')
    await flush()
    const contextInputs = host.querySelectorAll<HTMLInputElement>('#settings-model-context input')
    expect(contextInputs.length).toBeGreaterThan(0)
    await typeInto(contextInputs[0], '4096')
    expect(contextInputs[0].value).toBe('4096')

    for (let i = 0; i < 3; i++) {
      await go('登录')
      await go('图片识别')
      await go('出网代理')
      await go('会话')
    }

    expect(skipToggle().getAttribute('aria-checked')).toBe('true')
    expect(permissionSelect().value).toBe('judge')
    expect(votesSelect().value).toBe('3')
    expect(customNameInput().value).toBe('草稿模型')
    expect(proxyInput().value).toBe('127.0.0.1:7890')
    expect(host.querySelectorAll<HTMLInputElement>('#settings-model-context input')[0].value).toBe('4096')
    expect(api.saveSettings).toHaveBeenCalled()
    expect(api.saveSettings.mock.calls[api.saveSettings.mock.calls.length - 1]?.[0]).toMatchObject({
      sessionId: 'A',
      permissionMode: 'judge',
      harmJudgeVotes: 3,
      skipLogin: true,
    })
    expect(api.setWebProxy).not.toHaveBeenCalled()
    expect(api.saveModelContextSettings).not.toHaveBeenCalled()
    expect(onSwitchModel).not.toHaveBeenCalled()
    // Same DOM nodes for the drafts we care about most for mount stability.
    const proxyNode = proxyInput()
    await go('关于与更新')
    await go('出网代理')
    expect(proxyInput()).toBe(proxyNode)
  })

  it('autosavesFourFieldsWithTheCapturedSessionId', async () => {
    await mount(page({ initial: { skipLogin: false, harmJudgeModel: 'judge-x', harmJudgeVotes: 1 }, session: info({ permissionMode: 'judge' }) }))
    await act(async () => { skipToggle().click() })
    await act(async () => {
      votesSelect().value = '5'
      votesSelect().dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flush()
    expect(api.saveSettings.mock.calls[api.saveSettings.mock.calls.length - 1]?.[0]).toEqual({
      sessionId: 'A',
      permissionMode: 'judge', harmJudgeModel: 'judge-x', harmJudgeVotes: 5, skipLogin: true,
    })
    expect(api.setWebProxy).not.toHaveBeenCalled()
    expect(api.saveModelContextSettings).not.toHaveBeenCalled()
    expect(api.saveImageGenerationSettings).not.toHaveBeenCalled()
    expect(api.saveVisionSettings).not.toHaveBeenCalled()
    expect(api.setContextAudit).not.toHaveBeenCalled()
    expect(api.saveRecorderSettings).not.toHaveBeenCalled()
  })

  it('rebasesAChangedChatPermissionBeforeSavingAnotherField', async () => {
    await mount(page({ session: info({ permissionMode: 'interactive' }) }))
    await act(async () => root.render(page({ session: info({ permissionMode: 'judge' }) })))
    await flush()

    await act(async () => { skipToggle().click() })
    await flush()

    expect(api.saveSettings.mock.calls[api.saveSettings.mock.calls.length - 1]?.[0]).toMatchObject({
      sessionId: 'A',
      permissionMode: 'judge',
      skipLogin: true,
    })
  })

  it('keepsSaveFailureVisibleAfterNavigation', async () => {
    api.saveSettings.mockRejectedValueOnce(new Error('保存失败：磁盘已满'))
    await mount(page())
    await act(async () => { skipToggle().click() })
    await flush()
    expect(host.textContent).toContain('保存失败：磁盘已满')
    expect(host.textContent).not.toContain('✓ 已保存')
    await go('图片识别')
    await go('登录')
    expect(host.textContent).toContain('保存失败：磁盘已满')
  })

  it('autosavesProxyWithoutTheRemovedBottomButton', async () => {
    await mount(page())
    await go('出网代理')
    await typeInto(proxyInput(), 'socks5://127.0.0.1:1080')
    await act(async () => {
      proxyInput().focus()
      proxyInput().blur()
      await Promise.resolve()
      await Promise.resolve()
    })
    await flush()
    expect(api.setWebProxy).toHaveBeenCalledWith('socks5://127.0.0.1:1080')
    expect(api.saveSettings).not.toHaveBeenCalled()
  })
})

describe('SettingsPage disable matrix', () => {
  it('keepsDirectoryUsableWhileBusyAndAccountSync', async () => {
    // accountBusy while resolving: delay passportStatus.
    let releaseStatus!: (v: { loggedIn: boolean }) => void
    api.passportStatus.mockReturnValue(new Promise((r) => { releaseStatus = r }))
    await mount(page({ busy: true }))
    expect(Array.from(host.querySelectorAll('button')).some((button) => button.textContent === '保存设置')).toBe(false)
    expect(host.textContent).toContain('对话进行中，暂不能切换模型')
    expect(navButton('图片生成')).not.toBeNull()
    await go('图片生成')
    await go('出网代理')
    expect(host.querySelector('#settings-proxy')).not.toBeNull()
    expect(skipToggle().disabled).toBe(false)
    await act(async () => { releaseStatus({ loggedIn: false }) })
    await flush()
  })

  it('disablesBottomSaveWhileTenantNotReady', async () => {
    api.passportStatus.mockResolvedValue({ loggedIn: true, userId: 'u1', name: '测' })
    api.passportTenants.mockResolvedValue([{ id: 't1', name: '租户一', leaf: true }])
    api.activeTenant.mockResolvedValue('t1')
    // Keep models resolving so ready stays false after tenant is known.
    api.passportModels.mockReturnValue(new Promise(() => {}))
    await mount(page())
    expect(Array.from(host.querySelectorAll('button')).some((button) => button.textContent === '保存设置')).toBe(false)
    expect(navButton('会话')).not.toBeNull()
    await go('工作区')
    expect(host.querySelector('#settings-workspace')!.textContent).toContain('C:\\ws')
  })
})
