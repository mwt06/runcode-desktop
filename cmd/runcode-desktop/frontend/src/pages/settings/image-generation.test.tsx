// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ImageGenerationCatalog, ImageGenerationSettings, SaveImageGenerationSettingsRequest } from '@/core/bridge'

const api = vi.hoisted(() => ({
  listImageGenerationModels: vi.fn(),
  getImageGenerationSettings: vi.fn(),
  saveImageGenerationSettings: vi.fn(),
}))
vi.mock('@/core/bridge', () => ({ ...api, errText: (e: unknown) => (e instanceof Error ? e.message : String(e)) }))

import { ImageGenerationSection } from './image-generation'

const bridge = 'https://bridge.example'
const ref = (name: string, tenantId = 't1') => ({ kind: 'platform', name, bridge, tenantId })
const profile = { available: true, supportedSizes: ['1024x1024', '2048x2048'], defaultSize: '1024x1024', responseFormats: ['url'] }
const catalog = (tenantId = 't1'): ImageGenerationCatalog => ({
  scopeId: 'scope-' + tenantId,
  models: [
    { model: ref('img-a', tenantId), provider: 'v', profile },
    { model: ref('img-off', tenantId), provider: 'v', profile: { ...profile, available: false, unavailableReason: '尚未完成兼容验证' } },
  ],
})
const settings = (patch: Partial<ImageGenerationSettings> = {}): ImageGenerationSettings => ({ scopeId: 'scope-t1', revision: 0, defaultSize: '', ...patch })

let host: HTMLDivElement, root: Root
beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  api.listImageGenerationModels.mockReset()
  api.getImageGenerationSettings.mockReset()
  api.saveImageGenerationSettings.mockReset()
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

async function mount(tenantId = 't1') {
  await act(async () => root.render(<ImageGenerationSection tenantId={tenantId} />))
  await act(async () => { await Promise.resolve() })
}
// The wrapping <label> names each select; order is model, then size.
const modelSelect = () => host.querySelectorAll<HTMLSelectElement>('select')[0]
const sizeSelect = () => host.querySelectorAll<HTMLSelectElement>('select')[1]
const button = (text: string) => [...host.querySelectorAll('button')].find((b) => b.textContent?.includes(text))!
async function choose(select: HTMLSelectElement, value: string) {
  await act(async () => {
    select.value = value
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

describe('ImageGenerationSection', () => {
  it('只有可用生图模型可选，不可用项带原因且禁用；不自动选第一项', async () => {
    api.listImageGenerationModels.mockResolvedValue(catalog())
    api.getImageGenerationSettings.mockResolvedValue(settings())
    await mount()
    const options = [...modelSelect().options]
    expect(modelSelect().value).toBe('')
    expect(options.find((o) => o.textContent?.includes('img-a'))?.disabled).toBe(false)
    const off = options.find((o) => o.textContent?.includes('img-off'))
    expect(off?.disabled).toBe(true)
    expect(off?.textContent).toContain('尚未完成兼容验证')
    expect(host.textContent).toContain('不改变当前聊天模型')
    expect(host.textContent).toContain('生图模型选择')
    expect(host.textContent).toContain('图片尺寸设置')
    expect(host.textContent).not.toContain('每次生成会计费')
  })

  it('空目录、加载失败、原选择失效三种状态区分', async () => {
    api.listImageGenerationModels.mockResolvedValue({ scopeId: 'scope-t1', models: [] })
    api.getImageGenerationSettings.mockResolvedValue(settings())
    await mount()
    expect(host.textContent).toContain('当前租户暂无可用生图模型')
    expect(host.textContent).not.toContain('加载失败')

    api.listImageGenerationModels.mockRejectedValue(new Error('获取平台模型目录失败'))
    await act(async () => button('刷新模型列表').click())
    await act(async () => { await Promise.resolve() })
    expect(host.textContent).toContain('加载失败')
    expect(host.textContent).not.toContain('当前租户暂无可用生图模型')

    api.listImageGenerationModels.mockResolvedValue(catalog())
    api.getImageGenerationSettings.mockResolvedValue(settings({ revision: 2, defaultModel: ref('img-off'), defaultSize: '1024x1024' }))
    await act(async () => button('刷新模型列表').click())
    await act(async () => { await Promise.resolve() })
    expect(host.textContent).toContain('已不可用')
    expect(modelSelect().value).toContain('img-off')
  })

  it('多尺寸模型默认不固定尺寸，可保存空尺寸；也可改固定尺寸', async () => {
    api.listImageGenerationModels.mockResolvedValue(catalog())
    api.getImageGenerationSettings.mockResolvedValue(settings())
    api.saveImageGenerationSettings.mockResolvedValueOnce(settings({ revision: 1, defaultModel: ref('img-a'), defaultSize: '' }))
    await mount()
    await choose(modelSelect(), modelSelect().options[1].value)
    const firstSize = sizeSelect().options[0]
    expect(firstSize.value).toBe('')
    expect(firstSize.textContent).toContain('不固定尺寸')
    expect(sizeSelect().title).toContain('由提示词和生图模型能力共同决定')
    expect(sizeSelect().value).toBe('')
    await act(async () => button('保存').click())
    expect(api.saveImageGenerationSettings.mock.calls[0][0]).toMatchObject({ defaultModel: ref('img-a'), defaultSize: '' })
    await choose(sizeSelect(), '2048x2048')
    api.saveImageGenerationSettings.mockResolvedValueOnce(settings({ revision: 2, defaultModel: ref('img-a'), defaultSize: '2048x2048' }))
    await act(async () => button('保存').click())
    expect(api.saveImageGenerationSettings.mock.calls[1][0]).toMatchObject({ defaultSize: '2048x2048' })
  })

  it('仅一种尺寸的模型不提供不固定尺寸，并默认选中该尺寸', async () => {
    const single = {
      scopeId: 'scope-t1',
      models: [{
        model: ref('img-one'),
        provider: 'v',
        profile: { available: true, supportedSizes: ['1024x1024'], defaultSize: '1024x1024', responseFormats: ['url'] },
      }],
    }
    api.listImageGenerationModels.mockResolvedValue(single)
    api.getImageGenerationSettings.mockResolvedValue(settings())
    api.saveImageGenerationSettings.mockResolvedValueOnce(settings({ revision: 1, defaultModel: ref('img-one'), defaultSize: '1024x1024' }))
    await mount()
    await choose(modelSelect(), modelSelect().options[1].value)
    expect([...sizeSelect().options].map((o) => o.value)).toEqual(['1024x1024'])
    expect(sizeSelect().value).toBe('1024x1024')
    expect(sizeSelect().textContent).not.toContain('不固定尺寸')
    await act(async () => button('保存').click())
    expect(api.saveImageGenerationSettings.mock.calls[0][0]).toMatchObject({ defaultModel: ref('img-one'), defaultSize: '1024x1024' })
  })

  it('显式保存；保存失败保留草稿且不改已生效值', async () => {
    api.listImageGenerationModels.mockResolvedValue(catalog())
    api.getImageGenerationSettings.mockResolvedValue(settings())
    api.saveImageGenerationSettings.mockRejectedValueOnce(new Error('图片生成设置已变化，请刷新后重试'))
    await mount()
    await choose(modelSelect(), modelSelect().options[1].value)
    expect(api.saveImageGenerationSettings).not.toHaveBeenCalled()
    expect(sizeSelect().value).toBe('')
    await choose(sizeSelect(), '2048x2048')
    await act(async () => button('保存').click())
    const req = api.saveImageGenerationSettings.mock.calls[0][0] as SaveImageGenerationSettingsRequest
    expect(req).toMatchObject({ tenantId: 't1', scopeId: 'scope-t1', expectedRevision: 0, defaultModel: ref('img-a'), defaultSize: '2048x2048' })
    expect(host.textContent).toContain('图片生成设置已变化')
    expect(modelSelect().value).toContain('img-a')
    expect(host.textContent).not.toContain('已保存')

    api.saveImageGenerationSettings.mockResolvedValueOnce(settings({ revision: 1, defaultModel: ref('img-a'), defaultSize: '2048x2048' }))
    await act(async () => button('保存').click())
    expect(host.textContent).toContain('已保存')
    expect(host.textContent).toContain('下一轮')
  })

  it('切换租户后旧租户的迟到结果被忽略', async () => {
    let releaseOld!: (c: ImageGenerationCatalog) => void
    api.listImageGenerationModels.mockImplementation((tenant: string) => tenant === 't1'
      ? new Promise<ImageGenerationCatalog>((r) => { releaseOld = r })
      : Promise.resolve(catalog('t2')))
    api.getImageGenerationSettings.mockImplementation(async (tenant: string) => settings({ scopeId: 'scope-' + tenant }))
    await act(async () => root.render(<ImageGenerationSection tenantId="t1" />))
    await mount('t2')
    await act(async () => { releaseOld(catalog('t1')); await Promise.resolve() })
    const keys = [...modelSelect().options].map((o) => o.value).filter(Boolean)
    expect(keys.every((k) => k.includes('"t2"'))).toBe(true)
  })

  it('可清除选择', async () => {
    api.listImageGenerationModels.mockResolvedValue(catalog())
    api.getImageGenerationSettings.mockResolvedValue(settings({ revision: 1, defaultModel: ref('img-a'), defaultSize: '1024x1024' }))
    api.saveImageGenerationSettings.mockResolvedValue(settings({ revision: 2 }))
    await mount()
    await choose(modelSelect(), '')
    await act(async () => button('保存').click())
    expect(api.saveImageGenerationSettings.mock.calls[0][0]).toMatchObject({ expectedRevision: 1, defaultModel: undefined, defaultSize: '' })
  })
})
