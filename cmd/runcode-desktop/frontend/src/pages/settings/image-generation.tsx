// 「图片生成」设置小节：当前平台/租户下的生图模型与尺寸。显式保存（切换下拉
// 不触发网络校验与落盘）；目录只列网关明确标为 image_generation 的模型。只影响生成
// 图片，不改变聊天模型；保存后下一轮生效，进行中的回合固定原模型与尺寸。
import { useCallback, useEffect, useState } from 'react'
import {
  errText, getImageGenerationSettings, listImageGenerationModels, saveImageGenerationSettings,
  type ImageGenerationCatalog, type ImageGenerationSettings,
} from '@/core/bridge'
import {
  allowsUnfixedSize, defaultDraftSize, imageGenerationModelKey, settingsSelection,
} from '@/core/image-generation'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { InlineError } from '@/ui/feedback'
import { LABEL_CLS, SelectField } from '@/ui/fields'
import { Section } from './section'

export function ImageGenerationSection({ tenantId }: { tenantId: string }) {
  const [catalog, setCatalog] = useState<ImageGenerationCatalog | null>(null)
  const [settings, setSettings] = useState<ImageGenerationSettings | null>(null)
  const [catalogError, setCatalogError] = useState('')
  const [loading, setLoading] = useState(false)
  const [draftKey, setDraftKey] = useState('')
  const [draftSize, setDraftSize] = useState('')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)
  // generation 让切换租户/账号后的迟到结果作废。它不指向 DOM 节点，卸载时作废在途请求是有意的。
  const [generation] = useState(() => ({ seq: 0 }))

  const load = useCallback(async () => {
    const gen = ++generation.seq
    setLoading(true)
    setSaving(false)
    setCatalogError('')
    setSaveError('')
    setSaved(false)
    const [catalogResult, settingsResult] = await Promise.allSettled([listImageGenerationModels(tenantId), getImageGenerationSettings(tenantId)])
    if (gen !== generation.seq) return
    const nextCatalog = catalogResult.status === 'fulfilled' ? catalogResult.value ?? null : null
    const nextSettings = settingsResult.status === 'fulfilled' ? settingsResult.value ?? null : null
    setCatalog(nextCatalog)
    setSettings(nextSettings)
    // An empty answer or a message-less rejection is a failed read too; never
    // leave the section silently disabled.
    const failed = '未能读取生图模型或设置'
    setCatalogError(catalogResult.status === 'rejected' ? errText(catalogResult.reason) || failed
      : settingsResult.status === 'rejected' ? errText(settingsResult.reason) || failed
        : !nextCatalog || !nextSettings ? failed : '')
    const sel = settingsSelection(nextCatalog, nextSettings)
    setDraftKey(sel.key)
    setDraftSize(sel.size)
    setLoading(false)
  }, [tenantId, generation])
  useEffect(() => {
    void load()
    return () => { generation.seq++ }
  }, [load, generation])

  const models = catalog?.models ?? []
  const confirmed = settingsSelection(catalog, settings)
  const draftModel = models.find((m) => imageGenerationModelKey(m.model) === draftKey)
  const draftStale = !!draftKey && (!draftModel || !draftModel.profile.available)
  const sizes = draftModel?.profile.supportedSizes ?? []
  const unfixedOK = allowsUnfixedSize(sizes)
  const dirty = draftKey !== confirmed.key || (!!draftKey && draftSize !== confirmed.size)
  // 多尺寸允许空（不固定）；单尺寸必须落在唯一支持值上。
  const sizeOK = !draftKey || (unfixedOK && draftSize === '') || sizes.includes(draftSize)
  const canSave = !!settings && !!catalog && !saving && !loading && dirty && !draftStale && sizeOK

  function pickModel(key: string) {
    setSaved(false)
    setSaveError('')
    setDraftKey(key)
    const next = models.find((m) => imageGenerationModelKey(m.model) === key)
    setDraftSize(defaultDraftSize(next?.profile.supportedSizes))
  }

  async function save() {
    if (!settings || !catalog) return
    const gen = generation.seq
    setSaving(true)
    setSaveError('')
    setSaved(false)
    try {
      const next = await saveImageGenerationSettings({
        tenantId, scopeId: settings.scopeId, expectedRevision: settings.revision,
        defaultModel: draftModel?.model, defaultSize: draftModel ? draftSize : '',
      })
      if (gen !== generation.seq) return
      setSettings(next)
      const sel = settingsSelection(catalog, next)
      setDraftKey(sel.key)
      setDraftSize(sel.size)
      setSaved(true)
    } catch (e) {
      if (gen === generation.seq) setSaveError(errText(e) || '保存失败，请重试')
    } finally {
      if (gen === generation.seq) setSaving(false)
    }
  }

  const staleName = settings?.defaultModel?.name ?? ''
  return <Section title="图片生成" hint="显式保存，下一轮生效">
    <p className="text-[12px] text-muted">仅用于生成图片，不改变当前聊天模型。配置可用生图模型后，聊天模型会根据你的提示词判断是否调用生图工具。</p>
    <div className="text-[12px] text-muted">当前平台租户：<span className="font-mono text-ink2">{tenantId || '通行证默认租户'}</span></div>
    <label className={LABEL_CLS}>
      生图模型选择
      <SelectField value={draftKey} disabled={!catalog || !settings || saving || loading} onChange={pickModel}>
        <option value="">不使用（清除选择）</option>
        {draftKey && !draftModel && <option value={draftKey} disabled>{staleName} · 已不可用，请重新选择</option>}
        {models.map((m) => {
          const key = imageGenerationModelKey(m.model)
          return <option key={key} value={key} disabled={!m.profile.available}>
            {m.model.name}{m.provider ? ` · ${m.provider}` : ''}{m.profile.available ? '' : ` · 不可用：${m.profile.unavailableReason ?? ''}`}
          </option>
        })}
      </SelectField>
    </label>
    <label className={LABEL_CLS} title={unfixedOK ? '由提示词和生图模型能力共同决定' : undefined}>
      图片尺寸设置
      <SelectField
        value={draftSize}
        disabled={!draftModel || draftStale || saving || loading || sizes.length === 0}
        title={unfixedOK ? '由提示词和生图模型能力共同决定' : undefined}
        onChange={(v) => { setSaved(false); setDraftSize(v) }}
      >
        {!draftModel && <option value="">先选择模型</option>}
        {draftModel && sizes.length === 0 && <option value="">暂无可用尺寸</option>}
        {draftModel && unfixedOK && <option value="">不固定尺寸</option>}
        {draftModel && draftSize !== '' && !sizes.includes(draftSize) && <option value={draftSize} disabled>{draftSize} · 已不支持</option>}
        {sizes.map((s) => <option key={s} value={s}>{s.replace('x', ' × ')}</option>)}
      </SelectField>
    </label>
    {catalog && !catalogError && models.length === 0 && <p className="text-[12px] text-muted">当前租户暂无可用生图模型。</p>}
    {draftStale && <InlineError>原选择的生图模型已不可用，请重新选择；在重新选择前，生成图片会提示未配置，不会改用其他模型。</InlineError>}
    {catalogError && <InlineError>生图模型目录加载失败：{catalogError}</InlineError>}
    {saveError && <InlineError>{saveError}</InlineError>}
    <div className="flex items-center gap-3">
      <button type="button" className={`${BTN} ${BTN_PRIMARY} px-5 py-2`} disabled={!canSave} onClick={() => void save()}>{saving ? '保存中…' : '保存'}</button>
      <button type="button" className={`${BTN} px-4 py-2`} disabled={loading || saving} onClick={() => void load()}>{loading ? '正在加载…' : '刷新模型列表'}</button>
      {saved && <span role="status" className="text-green text-[13px]">✓ 已保存，下一轮生效</span>}
    </div>
  </Section>
}
