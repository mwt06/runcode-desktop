import { describe, expect, it } from 'vitest'
import { imageCapabilityLabel, imageCapabilityValue, imageModelCandidates, imageModelKey, platformVisionDefault } from './vision'
import { customModelDraftForEdit, toCustomModelSaveRequest } from './custom-models'
import type { CustomModel } from './bridge'

describe('image model routing preferences', () => {
  it('keeps false distinct from unknown, without inferring model names', () => {
    expect([true, false, undefined].map(imageCapabilityLabel)).toEqual(['支持图片', '仅文本', '未标注'])
    expect(['true', 'false', 'unknown', 'gpt-vision'].map(imageCapabilityValue)).toEqual([true, false, undefined, undefined])
  })
  it('offers only declared image models, retaining tenant and source identities', () => {
    const custom: CustomModel[] = [{ name: 'same', provider: 'openai', model: 'image', baseURL: '', supportsImages: true }]
    const rows = imageModelCandidates([{ id: 'same', ownedBy: '', supportsImages: true }, { id: 'guess-vision', ownedBy: '' }, { id: 'text', ownedBy: '', supportsImages: false }], custom, 'https://bridge', 'tenant-A')
    expect(rows).toHaveLength(2)
    expect(imageModelKey(rows[0].ref)).not.toEqual(imageModelKey(rows[1].ref))
    expect(imageModelKey(rows[0].ref)).not.toEqual(imageModelKey({ ...rows[0].ref, tenantId: 'tenant-B' }))
  })
  it('preserves false and explicitly clears a capability in an edited form', () => {
    const draft = customModelDraftForEdit({ name: 'text', model: 'm', baseURL: '', supportsImages: false })
    expect(toCustomModelSaveRequest(draft, 'text').supportsImages).toBe(false)
    expect(toCustomModelSaveRequest(draft, 'text').clearImageSupport).toBeUndefined()
    expect(toCustomModelSaveRequest({ ...draft, supportsImages: undefined }, 'text').clearImageSupport).toBe(true)
  })
})

describe('platform default image model', () => {
  it('requires an explicit unique default rather than choosing the first capable model', () => {
    const candidate = { id: 'qwen3-vl-plus', ownedBy: 'qwen', supportsImages: true }
    expect(platformVisionDefault([candidate]).model).toBeUndefined()
    expect(platformVisionDefault([{ ...candidate, local: true, localDefault: true }]).model).toBeUndefined()
    expect(platformVisionDefault([{ id: 'first', ownedBy: '', supportsImages: true }, { ...candidate, visionDefault: true }]).model?.id).toBe(candidate.id)
    expect(platformVisionDefault([{ ...candidate, visionDefault: true }, { ...candidate, visionDefault: true }]).error).toContain('多个')
  })
  it('does not silently override a local text-only flag or guess unknown capabilities', () => {
    for (const supportsImages of [false, undefined]) {
      expect(platformVisionDefault([{ id: 'default', ownedBy: '', visionDefault: true, supportsImages }]).error).toContain('未标为支持图片')
    }
    expect(platformVisionDefault([]).error).toContain('当前租户')
  })
})
