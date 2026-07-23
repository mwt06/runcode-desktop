import { type CustomModel, type SaveCustomModelRequest } from './bridge'

export type CustomModelProvider = 'openai' | 'anthropic'

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
// they used the OpenAI-compatible path, so keep that as the compatibility default.
export function customModelProvider(provider?: string): CustomModelProvider {
  return provider?.trim().toLowerCase() === 'anthropic' ? 'anthropic' : 'openai'
}

export function customModelProviderLabel(provider?: string): string {
  return customModelProvider(provider) === 'anthropic' ? 'Anthropic' : 'OpenAI 兼容'
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
