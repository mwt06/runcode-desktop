import { describe, expect, it } from 'vitest'
import {
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
