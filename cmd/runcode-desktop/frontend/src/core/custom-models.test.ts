import { describe, expect, it } from 'vitest'
import {
  CUSTOM_MODEL_PROVIDERS,
  customModelBaseURLHint,
  customModelDraftForEdit,
  customModelOptionSub,
  customModelProvider,
  customModelProviderLabel,
  codexAuthMode,
  usesChatGPTLogin,
  toCustomModelSaveRequest,
  type CustomModelDraft,
} from './custom-models'

const draft = (overrides: Partial<CustomModelDraft> = {}): CustomModelDraft => ({
  name: ' local ',
  provider: 'openai',
  model: ' model-id ',
  baseURL: ' http://localhost:11434/v1 ',
  authMode: 'chatgpt',
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

  it('offers the three engine provider ids plus the shell-level codex', () => {
    // 前三个必须与引擎 llm 注册表逐字一致；codex 是外壳自己的，后端解析时翻译成
    // openai-responses(见 core/custom-models.ts 的说明)。
    expect(CUSTOM_MODEL_PROVIDERS).toEqual(['openai', 'openai-responses', 'anthropic', 'codex'])
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
      authMode: 'chatgpt',
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

describe('codex profiles', () => {
  it('treats codex as a provider even though the engine has no such name', () => {
    expect(CUSTOM_MODEL_PROVIDERS).toContain('codex')
    expect(customModelProvider('codex')).toBe('codex')
    expect(customModelProviderLabel('codex')).toContain('Codex')
  })

  it('defaults the auth mode to the ChatGPT subscription', () => {
    expect(codexAuthMode()).toBe('chatgpt')
    expect(codexAuthMode('')).toBe('chatgpt')
    expect(codexAuthMode('nonsense')).toBe('chatgpt')
    expect(codexAuthMode('APIKey')).toBe('apikey')
  })

  it('only asks for a ChatGPT login on codex profiles that chose it', () => {
    expect(usesChatGPTLogin({ provider: 'codex', authMode: 'chatgpt' })).toBe(true)
    expect(usesChatGPTLogin({ provider: 'codex', authMode: 'apikey' })).toBe(false)
    expect(usesChatGPTLogin({ provider: 'openai', authMode: 'chatgpt' })).toBe(false)
  })

  it('drops the base URL and key when the subscription supplies both', () => {
    // Stale input from before the user switched auth mode must not be saved:
    // the endpoint is fixed and the credential comes from the login.
    const req = toCustomModelSaveRequest(draft({
      provider: 'codex',
      authMode: 'chatgpt',
      baseURL: ' https://left-over.example ',
      apiKey: 'sk-left-over',
    }))
    expect(req.baseURL).toBe('')
    expect(req.apiKey).toBeUndefined()
    expect(req.authMode).toBe('chatgpt')
  })

  it('keeps the relay endpoint and key when using an API key', () => {
    const req = toCustomModelSaveRequest(draft({
      provider: 'codex',
      authMode: 'apikey',
      baseURL: ' https://relay.example/v1 ',
      apiKey: 'sk-relay',
    }))
    expect(req.baseURL).toBe('https://relay.example/v1')
    expect(req.apiKey).toBe('sk-relay')
    expect(req.authMode).toBe('apikey')
  })

  it('omits the auth mode for non-codex providers', () => {
    // The field is meaningless outside codex; sending it would leave the next
    // reader guessing whether it counts.
    expect(toCustomModelSaveRequest(draft({ provider: 'openai', authMode: 'apikey' })).authMode).toBeUndefined()
  })
})
