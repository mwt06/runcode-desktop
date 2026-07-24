import { type CustomModel, type SaveCustomModelRequest } from './bridge'

export type CustomModelProvider = 'openai' | 'openai-responses' | 'anthropic'

// id 必须与引擎 llm 注册表里的 provider 名逐字一致：后端把它原样交给
// engine.Config.Provider，Go 侧只用 llm.IsRegistered 兜底，不会翻译别名。
// OpenAI 有两套线上协议，同一个厂商但请求体与事件流完全不同，所以是两个 id。
const PROVIDER_LABELS: Record<CustomModelProvider, string> = {
  openai: 'OpenAI 兼容',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic',
}

// 表单下拉的顺序即此处的声明顺序，新增服务商只改上面的表。
export const CUSTOM_MODEL_PROVIDERS = Object.keys(PROVIDER_LABELS) as CustomModelProvider[]

export type CustomModelDraft = {
  name: string
  provider: CustomModelProvider
  model: string
  baseURL: string
  apiKey: string
  clearAPIKey: boolean
}

export const emptyCustomModelDraft = (): CustomModelDraft => ({
  name: '',
  provider: 'openai',
  model: '',
  baseURL: '',
  apiKey: '',
  clearAPIKey: false,
})

// Persisted models created before provider selection existed have no provider;
// they used the OpenAI-compatible path, so keep that as the compatibility default
// — as does anything unrecognized, which is the only id that could have been
// written by an older build.
export function customModelProvider(provider?: string): CustomModelProvider {
  const id = provider?.trim().toLowerCase()
  return id && id in PROVIDER_LABELS ? (id as CustomModelProvider) : 'openai'
}

export function customModelProviderLabel(provider?: string): string {
  return PROVIDER_LABELS[customModelProvider(provider)]
}

// Base URL 留空时引擎用该服务商的官方端点；填了就是 API 根，末尾的 /v1 漏掉或
// 把整段 /chat/completions 粘进来引擎都会纠正，所以提示词只给期望的形状。
export function customModelBaseURLHint(provider: CustomModelProvider): string {
  return provider === 'anthropic'
    ? 'Base URL（留空使用官方端点）'
    : 'Base URL（如 https://host/v1；留空使用官方端点）'
}

export function customModelOptionSub(model: CustomModel): string {
  return `${customModelProviderLabel(model.provider)} · ${model.model}`
}

// API keys are write-only across the desktop bridge: an edit starts with an empty
// field even when the profile reports hasAPIKey, so the renderer never receives or
// re-displays the saved secret.
export function customModelDraftForEdit(model: CustomModel): CustomModelDraft {
  return {
    name: model.name,
    provider: customModelProvider(model.provider),
    model: model.model,
    baseURL: model.baseURL,
    apiKey: '',
    clearAPIKey: false,
  }
}

export function toCustomModelSaveRequest(draft: CustomModelDraft, originalName?: string): SaveCustomModelRequest {
  const apiKey = draft.clearAPIKey ? '' : draft.apiKey
  return {
    originalName: originalName || undefined,
    name: draft.name.trim(),
    provider: draft.provider,
    model: draft.model.trim(),
    baseURL: draft.baseURL.trim(),
    apiKey: apiKey || undefined,
    clearAPIKey: draft.clearAPIKey || undefined,
  }
}
