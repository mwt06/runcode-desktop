import { describe, expect, it } from 'vitest'
import { toModelOptions } from './model-picker'
import type { CustomModel, PassportModel } from '@/core/bridge'

describe('toModelOptions', () => {
  it('平台模型在前、自定义模型在后,各带 kind', () => {
    const platform: PassportModel[] = [{ id: 'glm-4.6', ownedBy: 'zhipu' }]
    const custom: CustomModel[] = [{ name: 'GPT-5', provider: 'openai-responses', model: 'gpt-5', baseURL: '' }]
    const opts = toModelOptions(platform, custom)
    expect(opts.map((o) => o.kind)).toEqual(['platform', 'custom'])
    expect(opts[0]).toMatchObject({ id: 'glm-4.6', label: 'glm-4.6', sub: 'zhipu' })
    // 自定义项:id 是档名(交给 SwitchModel),modelId 是底层模型 id(标记当前项用),
    // sub 带上底层模型 id 便于识别。
    expect(opts[1]).toMatchObject({ id: 'GPT-5', label: 'GPT-5', modelId: 'gpt-5' })
    expect(opts[1].sub).toContain('gpt-5')
  })

  it('两侧为空时得到空列表', () => {
    expect(toModelOptions([], [])).toEqual([])
  })

  it('仅生图模型不出现在任何文本模型候选里', () => {
    const profile = { available: true, supportedSizes: ['1024x1024'], defaultSize: '1024x1024', responseFormats: ['url'] }
    const platform: PassportModel[] = [
      { id: 'chat', ownedBy: 'p' },
      { id: 'image-only', ownedBy: 'v', capabilities: ['image_generation'], imageGeneration: profile },
      { id: 'dual', ownedBy: 'p', capabilities: ['chat', 'image_generation'], imageGeneration: profile },
    ]
    expect(toModelOptions(platform, []).map((o) => o.id)).toEqual(['chat', 'dual'])
  })
})
