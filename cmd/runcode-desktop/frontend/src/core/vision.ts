import type { CustomModel, ModelReference, PassportModel } from './bridge'

export function imageCapabilityLabel(value?: boolean): string {
  return value === true ? '支持图片' : value === false ? '仅文本' : '未标注'
}
export function imageCapabilityValue(value: string): boolean | undefined {
  return value === 'true' ? true : value === 'false' ? false : undefined
}
export function imageModelKey(ref?: ModelReference): string {
  return ref ? JSON.stringify([ref.kind, ref.name, ref.bridge ?? '', ref.tenantId ?? '']) : ''
}
export function imageModelCandidates(platform: PassportModel[], custom: CustomModel[], bridge: string, tenantId: string) {
  return [
    ...platform.filter((m) => m.supportsImages === true).map((m) => ({ label: m.id, ref: { kind: 'platform', name: m.id, bridge, tenantId } satisfies ModelReference })),
    ...custom.filter((m) => m.supportsImages === true).map((m) => ({ label: `${m.name} · 自定义`, ref: { kind: 'custom', name: m.name } satisfies ModelReference })),
  ]
}

// Do not pick by list order, model name, or the unrelated OA localDefault flag.
export function platformVisionDefault(models: PassportModel[]): { model?: PassportModel; error?: string } {
  const defaults = models.filter((m) => m.visionDefault)
  if (defaults.length === 0) return { error: '平台未配置默认识图模型，或当前租户无权使用' }
  if (defaults.length !== 1) return { error: '平台配置了多个默认识图模型，请联系管理员修正' }
  const model = defaults[0]
  if (!model.id || model.supportsImages !== true) return { error: `平台默认识图模型 ${model.id} 未标为支持图片，请检查平台配置或本机覆盖` }
  return { model }
}
