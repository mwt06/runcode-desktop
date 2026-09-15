import { type CustomModel, type SaveCustomModelRequest } from './bridge'

export type CustomModelProvider = 'openai' | 'openai-responses' | 'anthropic' | 'codex'

// id 必须与引擎 llm 注册表里的 provider 名逐字一致：后端把它原样交给
// engine.Config.Provider，Go 侧只用 llm.IsRegistered 兜底，不会翻译别名。
// OpenAI 有两套线上协议，同一个厂商但请求体与事件流完全不同，所以是两个 id。
//
// codex 是唯一的例外：引擎注册表里没有它，后端解析时翻译成 openai-responses 并把
// Base URL 指向本地代理(补上游要的凭据与指纹头)。对用户它就是一种服务商，对引擎
// 它根本不存在——两边都不必知道对方的说法。
const PROVIDER_LABELS: Record<CustomModelProvider, string> = {
  openai: 'OpenAI 兼容',
  'openai-responses': 'OpenAI Responses',
  anthropic: 'Anthropic',
  codex: 'Codex（ChatGPT / 中转）',
}

// 表单下拉的顺序即此处的声明顺序，新增服务商只改上面的表。
export const CUSTOM_MODEL_PROVIDERS = Object.keys(PROVIDER_LABELS) as CustomModelProvider[]

// Codex 的认证方式：用登录的 ChatGPT 订阅，还是一把连第三方中转的 API 密钥。
export type CodexAuthMode = 'chatgpt' | 'apikey'

export type CustomModelDraft = {
  name: string
  provider: CustomModelProvider
  model: string
  baseURL: string
  authMode: CodexAuthMode
  apiKey: string
  clearAPIKey: boolean
}

export const emptyCustomModelDraft = (): CustomModelDraft => ({
  name: '',
  provider: 'openai',
  model: '',
  baseURL: '',
  authMode: 'chatgpt',
  apiKey: '',
  clearAPIKey: false,
})

// codexAuthMode 归一化存下来的认证方式。只有 codex 有这个概念；其余服务商一律
// 报 chatgpt(草稿的默认值)，但 toCustomModelSaveRequest 不会把它发出去——与后端
// normalizeCodexAuthMode 同一套口径：字段只对 codex 有意义。
export function codexAuthMode(mode?: string): CodexAuthMode {
  return mode?.trim().toLowerCase() === 'apikey' ? 'apikey' : 'chatgpt'
}

// usesChatGPTLogin 判断这份草稿要不要走 ChatGPT 登录。是的话表单收起 Base URL 与
// 密钥两栏——那两样由登录与固定端点提供，留着只会让人以为需要填。
export function usesChatGPTLogin(draft: Pick<CustomModelDraft, 'provider' | 'authMode'>): boolean {
  return draft.provider === 'codex' && draft.authMode === 'chatgpt'
}

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
  if (provider === 'codex') {
    return 'Base URL（第三方 Codex 中转地址）'
  }
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
    authMode: codexAuthMode(model.authMode),
    apiKey: '',
    clearAPIKey: false,
  }
}

export function toCustomModelSaveRequest(draft: CustomModelDraft, originalName?: string): SaveCustomModelRequest {
  // 走 ChatGPT 登录时不带 Base URL 与密钥：端点是固定的，凭据来自登录。把表单里
  // 可能残留的旧值一并丢掉，免得切换认证方式后还留着上一种的输入。
  const chatgpt = usesChatGPTLogin(draft)
  const apiKey = draft.clearAPIKey || chatgpt ? '' : draft.apiKey
  return {
    originalName: originalName || undefined,
    name: draft.name.trim(),
    provider: draft.provider,
    model: draft.model.trim(),
    baseURL: chatgpt ? '' : draft.baseURL.trim(),
    authMode: draft.provider === 'codex' ? draft.authMode : undefined,
    apiKey: apiKey || undefined,
    clearAPIKey: draft.clearAPIKey || undefined,
  }
}
