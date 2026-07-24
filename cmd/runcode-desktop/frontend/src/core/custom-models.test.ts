import { describe, expect, it } from 'vitest'
import {
  CUSTOM_MODEL_PROVIDERS,
  customModelBaseURLHint,
  customModelDraftForEdit,
  customModelOptionSub,
  customModelProvider,
  customModelProviderLabel,
  toCustomModelSaveRequest,
  type CustomModelDraft,
} from './custom-models'

const draft = (overrides: Partial<CustomModelDraft> = {}): CustomModelDraft => ({
  name: ' local ',
  provider: 'openai',
  model: ' model-id ',
  baseURL: ' http://localhost:11434/v1 ',
  apiKey: '',
  clearAPIKey: false,
  ...overrides,
})

describe('custom model providers', () => {
  it('defaults missing and unknown legacy providers to OpenAI-compatible', () => {
    expect(customModelProvider()).toBe('openai')
    expect(customModelProvider('')).toBe('openai')
    expect(customModelProvider('legacy-provider')).toBe('openai')
  })

  it('normalizes Anthropic and renders provider labels', () => {
    expect(customModelProvider(' Anthropic ')).toBe('anthropic')
    expect(customModelProviderLabel('anthropic')).toBe('Anthropic')
    expect(customModelProviderLabel()).toBe('OpenAI 兼容')
    expect(customModelOptionSub({ name: 'n', provider: 'anthropic', model: 'claude', baseURL: '' })).toBe('Anthropic · claude')
  })

  it('keeps OpenAI 的两套协议 as distinct ids', () => {
    expect(customModelProvider(' OpenAI-Responses ')).toBe('openai-responses')
    expect(customModelProviderLabel('openai-responses')).toBe('OpenAI Responses')
    expect(customModelOptionSub({ name: 'n', provider: 'openai-responses', model: 'gpt-5', baseURL: '' }))
      .toBe('OpenAI Responses · gpt-5')
    // 近似拼写不能被静默当成有效 id：后端只认注册表里的名字。
    expect(customModelProvider('openai_responses')).toBe('openai')
    expect(customModelProvider('responses')).toBe('openai')
  })

  it('offers exactly the ids the engine registry accepts', () => {
    expect(CUSTOM_MODEL_PROVIDERS).toEqual(['openai', 'openai-responses', 'anthropic'])
    for (const id of CUSTOM_MODEL_PROVIDERS) {
      expect(customModelProvider(id)).toBe(id)
    }
  })

  it('hints a Base URL is optional for every provider', () => {
    expect(customModelBaseURLHint('anthropic')).toContain('留空')
    expect(customModelBaseURLHint('openai')).toContain('/v1')
    expect(customModelBaseURLHint('openai-responses')).toContain('/v1')
  })
})

describe('custom model editing', () => {
  it('never copies a returned key into the edit draft', () => {
    const got = customModelDraftForEdit({
      name: 'secured',
      provider: 'anthropic',
      model: 'claude',
      baseURL: 'https://example.test',
      hasAPIKey: true,
      apiKey: 'must-not-copy',
      apiKeyProtected: 'must-not-copy-either',
    })
    expect(got).toEqual({
      name: 'secured',
      provider: 'anthropic',
      model: 'claude',
      baseURL: 'https://example.test',
      apiKey: '',
      clearAPIKey: false,
    })
  })

  it('trims display fields while leaving an entered key byte-for-byte', () => {
    expect(toCustomModelSaveRequest(draft({ apiKey: ' secret with spaces ' }), 'old')).toEqual({
      originalName: 'old',
      name: 'local',
      provider: 'openai',
      model: 'model-id',
      baseURL: 'http://localhost:11434/v1',
      apiKey: ' secret with spaces ',
      clearAPIKey: undefined,
    })
  })

  it('omits an empty key so editing preserves the saved secret', () => {
    const req = toCustomModelSaveRequest(draft(), 'local')
    expect(req.apiKey).toBeUndefined()
    expect(req.clearAPIKey).toBeUndefined()
    expect(req.originalName).toBe('local')
  })

  it('turns explicit clear into an unambiguous request', () => {
    const req = toCustomModelSaveRequest(draft({ apiKey: 'ignored', clearAPIKey: true }), 'local')
    expect(req.apiKey).toBeUndefined()
    expect(req.clearAPIKey).toBe(true)
  })

  it('omits edit-only intent for a new model', () => {
    const req = toCustomModelSaveRequest(draft())
    expect(req.originalName).toBeUndefined()
    expect(req.clearAPIKey).toBeUndefined()
  })
})
