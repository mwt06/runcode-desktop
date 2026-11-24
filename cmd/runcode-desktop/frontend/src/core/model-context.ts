import type { CustomModel, ModelContextOverride, ModelReference, PassportModel } from './bridge'
import { isChatModel } from './image-generation'

export const CONTEXT_FIELDS = [
  { key: 'maxOutputTokens', label: '最大输出 Tokens', min: 1, hint: '限制单次输出；必须为正整数。' },
  { key: 'maxContextTokens', label: '上下文预算', min: 0, hint: '接近预算时自动总结较早对话，磁盘历史完整保留；0 关闭自动压缩。' },
  { key: 'maxHistoryMessages', label: '历史消息上限', min: 0, hint: '硬截断，仅保留最近 N 条；0 不按条数截断，建议优先使用自动压缩。' },
] as const
export type ContextField = typeof CONTEXT_FIELDS[number]['key']
export type ContextDraft = Record<ContextField, string>

export function modelContextKey(ref: ModelReference): string {
  return JSON.stringify([ref.kind, ref.name, ref.bridge ?? '', ref.tenantId ?? ''])
}

export function contextModelCandidates(platform: PassportModel[], custom: CustomModel[], tenantId: string) {
  return [
    ...platform.filter(isChatModel).map((m) => ({ ref: { kind: 'platform', name: m.id, tenantId } as ModelReference, label: `平台 · ${m.id}` })),
    ...custom.map((m) => ({ ref: { kind: 'custom', name: m.name } as ModelReference, label: `自定义 · ${m.name}` })),
  ]
}

export function contextDraft(overrides: ModelContextOverride): ContextDraft {
  return Object.fromEntries(CONTEXT_FIELDS.map(({ key }) => [key, overrides[key] == null ? '' : String(overrides[key])])) as ContextDraft
}

export function parseContextDraft(draft: ContextDraft): ModelContextOverride {
  const result: ModelContextOverride = {}
  for (const { key, label, min } of CONTEXT_FIELDS) {
    const text = draft[key].trim()
    if (!text) continue
    if (!/^\d+$/.test(text) || !Number.isSafeInteger(Number(text)) || Number(text) < min || Number(text) > 2147483647) {
      throw new Error(`${label} 必须为 ${min} 到 2147483647 的整数；留空表示继承默认值`)
    }
    result[key] = Number(text)
  }
  return result
}
