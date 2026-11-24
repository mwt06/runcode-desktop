import { describe, expect, it } from 'vitest'
import type { ImageGenerationCatalog, ImageGenerationResult, PassportModel } from './bridge'
import {
  allowsUnfixedSize, coerceDraftSize, defaultDraftSize,
  imageGenerationModelKey,
  isChatModel, isImageGenerateToolRunning, isImageGenerationModel, isImageGenerationResult,
  isGeneratedImageRel, isRasterImageGeneration, settingsSelection, shouldShowImageGenerationStage,
} from './image-generation'

const profile = { available: true, supportedSizes: ['1024x1024', '2048x2048'], defaultSize: '1024x1024', responseFormats: ['url'] }
const ref = (name: string, tenantId = 't1') => ({ kind: 'platform', name, bridge: 'https://bridge.example', tenantId })

describe('generated image paths', () => {
  it('统一识别工作区相对、Windows 和绝对路径下的生成图片目录', () => {
    expect(isGeneratedImageRel('生成图片/g/image.png')).toBe(true)
    expect(isGeneratedImageRel('生成图片\\g\\image.png')).toBe(true)
    expect(isGeneratedImageRel('D:/ws/生成图片/g/image.png')).toBe(true)
    expect(isGeneratedImageRel('notes/image.png')).toBe(false)
  })
})

describe('model purpose filters', () => {
  const models: PassportModel[] = [
    { id: 'legacy', ownedBy: 'p' },
    { id: 'chat', ownedBy: 'p', capabilities: ['chat'] },
    { id: 'seedream-like-name', ownedBy: 'v' },
    { id: 'image-only', ownedBy: 'v', capabilities: ['image_generation'], imageGeneration: profile },
    { id: 'dual', ownedBy: 'p', capabilities: ['chat', 'image_generation'], imageGeneration: profile },
    { id: 'vision-input', ownedBy: 'p', supportsImages: true },
  ]
  it('仅生图模型不进入文本用途；旧目录保持聊天', () => {
    expect(models.filter(isChatModel).map((m) => m.id)).toEqual(['legacy', 'chat', 'seedream-like-name', 'dual', 'vision-input'])
  })
  it('只认显式 image_generation 能力，不按名称或 supportsImages 猜', () => {
    expect(models.filter(isImageGenerationModel).map((m) => m.id)).toEqual(['image-only', 'dual'])
  })
})

describe('settingsSelection', () => {
  const catalog: ImageGenerationCatalog = {
    scopeId: 's1',
    models: [
      { model: ref('a'), provider: 'v', profile },
      { model: ref('off'), provider: 'v', profile: { ...profile, available: false, unavailableReason: '未验证' } },
    ],
  }
  it('没有保存时不自动选第一项', () => {
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 0, defaultSize: '' })).toEqual({ key: '', size: '', stale: false })
  })
  it('有效保存选中并带出尺寸', () => {
    const got = settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('a'), defaultSize: '2048x2048' })
    expect(got).toEqual({ key: imageGenerationModelKey(ref('a')), size: '2048x2048', stale: false })
  })
  it('原选择失效时保留不可用占位，不换成其他模型', () => {
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('off'), defaultSize: '1024x1024' }).stale).toBe(true)
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('gone'), defaultSize: '1024x1024' })).toMatchObject({ key: imageGenerationModelKey(ref('gone')), stale: true })
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('a', 'other'), defaultSize: '1024x1024' }).stale).toBe(true)
  })
  it('保存的尺寸已不支持时视为失效', () => {
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('a'), defaultSize: '4096x4096' }).stale).toBe(true)
  })
  it('allowsUnfixedSize 仅在多尺寸时为真；单尺寸 defaultDraftSize 钉死', () => {
    expect(allowsUnfixedSize(['1024x1024'])).toBe(false)
    expect(allowsUnfixedSize(['1024x1024', '2048x2048'])).toBe(true)
    expect(defaultDraftSize(['1024x1024'])).toBe('1024x1024')
    expect(defaultDraftSize(['1024x1024', '2048x2048'])).toBe('')
    expect(coerceDraftSize('', ['1024x1024'])).toBe('1024x1024')
    expect(coerceDraftSize('999x999', ['1024x1024', '2048x2048'])).toBe('')
  })

  it('空默认尺寸表示不固定，不算失效', () => {
    expect(settingsSelection(catalog, { scopeId: 's1', revision: 2, defaultModel: ref('a'), defaultSize: '' })).toEqual({
      key: imageGenerationModelKey(ref('a')), size: '', stale: false,
    })
  })
})

describe('results', () => {
  const ok: ImageGenerationResult = {
    version: 1, generationId: 'g1', status: 'succeeded', model: 'a', size: '1024x1024', accountingStatus: 'unverified',
    images: [{ path: '生成图片/g1/image.png', mimeType: 'image/png', bytes: 10, width: 1, height: 1 }],
  }
  it('只收窄出 version 1 的结构化结果', () => {
    expect(isImageGenerationResult(ok)).toBe(true)
    expect(isImageGenerationResult({ ...ok, version: 2 })).toBe(false)
    expect(isImageGenerationResult({ snapshotId: 'x', relPath: 'y' })).toBe(false)
    expect(isImageGenerationResult(null)).toBe(false)
  })
  it('生图工具进行中判定：完成／失败不算 running', () => {
    expect(isImageGenerateToolRunning({ toolName: 'image_generate', type: 'progress' })).toBe(true)
    expect(isImageGenerateToolRunning({ toolName: 'image_generate', type: 'started' })).toBe(true)
    expect(isImageGenerateToolRunning({ toolName: 'image_generate', type: 'completed' })).toBe(false)
    expect(isImageGenerateToolRunning({ toolName: 'image_generate', type: 'failed' })).toBe(false)
    expect(isImageGenerateToolRunning({ toolName: 'Bash', type: 'progress' })).toBe(false)
  })
  it('行下舞台：生成中／成功挂载，失败不挂', () => {
    expect(shouldShowImageGenerationStage({ toolName: 'image_generate', type: 'progress' })).toBe(true)
    expect(shouldShowImageGenerationStage({ toolName: 'image_generate', type: 'completed', data: ok })).toBe(true)
    expect(shouldShowImageGenerationStage({ toolName: 'image_generate', type: 'failed', data: { ...ok, status: 'failed', images: [] } })).toBe(false)
    expect(shouldShowImageGenerationStage({ toolName: 'Read', type: 'completed' })).toBe(false)
  })
  it('栅格门控：只认 image_generate + 受支持 MIME，不按模型名', () => {
    expect(isRasterImageGeneration({ toolName: 'image_generate' })).toBe(true)
    expect(isRasterImageGeneration({ toolName: 'Read' })).toBe(false)
    expect(isRasterImageGeneration({ toolName: 'image_generate' }, ok)).toBe(true)
    expect(isRasterImageGeneration({ toolName: 'image_generate' }, {
      ...ok, model: 'other-vendor', images: [{ ...ok.images![0], mimeType: 'image/jpeg' }],
    })).toBe(true)
    expect(isRasterImageGeneration({ toolName: 'image_generate' }, {
      ...ok, images: [{ ...ok.images![0], mimeType: 'image/svg+xml' }],
    })).toBe(false)
    expect(isRasterImageGeneration({ toolName: 'image_generate' }, {
      ...ok, status: 'failed', images: [],
    })).toBe(true)
  })
})
