import { useEffect, useRef, useState } from 'react'
import { errText, getVisionSettings, passportModels, saveVisionSettings, setPlatformImageCapability, type CustomModel, type ModelReference, type PassportModel, type VisionSettings } from '@/core/bridge'
import { imageCapabilityValue, imageModelCandidates, imageModelKey, platformVisionDefault } from '@/core/vision'
import { InlineError } from '@/ui/feedback'
import { SelectField } from '@/ui/fields'
import { Section } from './section'

export function VisionSection({ tenantId, platform, custom, onPlatformChanged }: {
  tenantId: string
  platform: PassportModel[]
  custom: CustomModel[]
  onPlatformChanged: (models: PassportModel[]) => void
}) {
  const [settings, setSettings] = useState<VisionSettings | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const generation = useRef(0)
  useEffect(() => {
    const gen = ++generation.current
    setSettings(null)
    setBusy(false)
    setError('')
    getVisionSettings().then((value) => {
      if (gen === generation.current) setSettings(value)
    }).catch((e: unknown) => { if (gen === generation.current) setError(errText(e)) })
    return () => { generation.current = gen + 1 }
  }, [tenantId, custom, platform])

  const candidates = imageModelCandidates(platform, custom, settings?.bridge ?? '', tenantId)
  const platformDefault = platformVisionDefault(platform)
  const current = settings?.disabled ? 'disabled' : imageModelKey(settings?.defaultModel)
  const following = !!settings && !settings.disabled && !settings.defaultModel
  const unavailable = !!settings?.defaultModel && !candidates.some((c) => imageModelKey(c.ref) === current)
  async function save(ref?: ModelReference, disabled = false) {
    const gen = generation.current
    setBusy(true)
    setError('')
    try {
      const next = await saveVisionSettings({ defaultModel: ref, disabled })
      if (gen === generation.current) setSettings(next)
    } catch (e) { if (gen === generation.current) setError(errText(e)) }
    finally { if (gen === generation.current) setBusy(false) }
  }
  async function mark(model: string, value: string) {
    if (!settings) return
    const gen = generation.current
    setBusy(true)
    setError('')
    try {
      const next = await setPlatformImageCapability({ model: { kind: 'platform', name: model, bridge: settings.bridge, tenantId }, supportsImages: imageCapabilityValue(value) })
      const models = await passportModels(tenantId)
      if (gen !== generation.current) return
      setSettings(next)
      onPlatformChanged(models ?? [])
    } catch (e) { if (gen === generation.current) setError(errText(e)) }
    finally { if (gen === generation.current) setBusy(false) }
  }
  return <Section title="图片识别" hint="自动保存，下个回合生效">
    <p className="text-[12px] text-muted">支持图片的主模型直接看原图；仅文本模型先由默认识图模型分析，再继续回答。原图和原提问都会保留。</p>
    <label className="flex flex-col gap-1 text-[12px] text-muted">
      默认图片识别模型
      <SelectField value={current} disabled={!settings || busy} onChange={(key) => void save(candidates.find((c) => imageModelKey(c.ref) === key)?.ref, key === 'disabled')}>
        <option value="">跟随平台 · {platformDefault.model?.id ?? "平台未配置或当前租户不可用"}</option>
        <option value="disabled">关闭识图兜底</option>
        {unavailable && <option value={current} disabled>{settings?.defaultModel?.name} · 其他租户或当前不可用</option>}
        {candidates.map((c) => <option key={imageModelKey(c.ref)} value={imageModelKey(c.ref)}>{c.label}</option>)}
      </SelectField>
    </label>
    <p className="text-[12px] text-muted">未手选时跟随会话所属平台和租户的默认模型；手动选择优先，关闭只影响识图兜底，不影响多模态主模型直接看图。图片和必要问题会发给识图模型，可能产生额外用量，不发送整段对话。读取 OA 数据后仍禁止第二识图连接。</p>
    {settings && candidates.length === 0 && <p className="text-[12px] text-muted">暂无可选识图模型：先在自定义模型或下方平台模型中标为「支持图片」。</p>}
    {platform.length > 0 && <details className="rounded-field border border-line2 p-3">
      <summary className="cursor-pointer text-[13px] text-ink">平台模型图片能力 · 当前租户</summary>
      <p className="text-[12px] text-muted mt-2">平台未声明时按未标注处理，保持原图直传；本机标记只针对当前平台和租户。</p>
      <div className="flex flex-col gap-2 mt-3">{platform.map((m) => {
        const override = settings?.platformCapabilities?.find((v) => v.model.name === m.id && (v.model.tenantId ?? '') === tenantId && v.model.bridge === settings.bridge)
        return <label key={m.id} className="flex items-center justify-between gap-3 text-[12px] text-ink2">
          <span className="font-mono truncate">{m.id}</span>
          <SelectField value={override ? String(override.supportsImages) : 'unknown'} disabled={!settings || busy} onChange={(v) => void mark(m.id, v)}>
            <option value="unknown">跟随平台 · 未声明则原图直传</option>
            <option value="true">支持图片</option>
            <option value="false">仅文本</option>
          </SelectField>
        </label>
      })}</div>
    </details>}
    {following && platformDefault.error && <InlineError>{platformDefault.error}；可手动选择支持图片的模型。</InlineError>}
    {busy && <p className="text-[12px] text-muted">正在保存…</p>}
    {error && <InlineError>{error}</InlineError>}
  </Section>
}
