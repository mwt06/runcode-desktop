// 设置页：按小节拆分——账号(通行证)、会话、自定义模型、联网工具代理、上下文长度。
// 拆分原则——进 saveSettings() 载荷的字段（会话）受控于 SettingsPage 本体；
// 各自直接落后端的小节（账号/代理/自定义模型草稿）状态内化，只把共享数据（平台
// 模型、自定义模型列表，二者合成模型候选）回流给父级。
//
// 分类目录只改滚动与焦点：不保存、不重建账号协调器、不改变业务小节的 key。
// 正文按完整清单顺序挂载；目录按可见清单过滤。录音按品牌决定是否挂载；审核探测
// 始终挂载，未知／不支持只隐藏包装与目录项。
import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from 'react'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { toModelOptions, type ModelOption } from '@/ui/model-picker'
import {
  errText, listCustomModels, saveSettings,
  type CustomModel, type PassportModel, type SessionInfo, type SettingsView,
} from '@/core/bridge'
import { Toggle } from '@/ui/toggle'
import { BRAND } from '@/core/brand'
import { Section } from './section'
import { AccountSection } from './account'
import { SessionSection } from './session'
import { CustomModelsSection } from './custom-models'
import { VisionSection } from './vision'
import { ImageGenerationSection } from './image-generation'
import { ProxySection } from './proxy'
import { RecorderSection } from './recorder'
import { ContextSection } from './context'
import { ContextAuditSection } from './context-audit'
import { RuntimesSection } from './runtimes'
import { AboutSection } from './about'
import { InlineError } from '@/ui/feedback'
import { PageShell } from '@/ui/layout'
import { type UpdateController } from '@/session/use-update'
import { useRuntimes } from '@/session/use-runtimes'
import {
  SETTINGS_CATEGORIES,
  buildSettingsNavigation,
  visibleSectionIds,
  type SettingsSectionId,
} from './navigation-model'
import { SettingsAnchor, SettingsGroup, SettingsNavigation } from './navigation'
import { useSettingsNavigation } from './use-settings-navigation'

function sectionMeta(id: SettingsSectionId) {
  for (const category of SETTINGS_CATEGORIES) {
    const section = category.sections.find((s) => s.id === id)
    if (section) return section
  }
  throw new Error(`unknown settings section: ${id}`)
}

export function SettingsPage({ initial, info, busy, update, onSaved, onSwitchModel, initialSection }: {
  initial: Partial<SettingsView>
  info: SessionInfo | null
  busy: boolean
  // update 由 App 持有（侧栏那颗小红点也读它），这里只是把它交给「关于与更新」。
  update: UpdateController
  onSaved: (info: SessionInfo) => void
  // Switch the live session's model/connection in place (platform ↔ custom), the same
  // path the composer's in-chat picker uses — no new session, history preserved.
  onSwitchModel: (choice: ModelOption) => void
  // 从会话工具条等入口直达某一节（如「图片生成」），不从目录顶开始。
  initialSection?: SettingsSectionId
}) {
  // The session's model is now switched live (see 模型 field below), so it is read
  // straight from the running session, not a form field. Permission mode still prefers
  // the live session. Saving this form never sends connection fields.
  const [harmJudgeModel, setHarmJudgeModel] = useState(initial.harmJudgeModel ?? '')
  const [harmJudgeVotes, setHarmJudgeVotes] = useState(initial.harmJudgeVotes ?? 1)
  const [permissionMode, setPermissionMode] = useState(info?.permissionMode || initial.permissionMode || 'interactive')
  // 免登录:关闭(默认)时启动强制走登录页;开启后未登录也可直接进表单用本地自定义模型。
  const [skipLogin, setSkipLogin] = useState(initial.skipLogin ?? false)
  const [saving, setSaving] = useState(false)
  const [accountBusy, setAccountBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState('')
  // 共享数据：平台模型（账号小节回流）+ 自定义模型列表（自定义模型小节维护），
  // 合成会话/判定模型选择器共用的候选列表。
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [activeTenantId, setActiveTenantId] = useState(initial.tenantId ?? '')
  const [accountReady, setAccountReady] = useState(false)
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  // 审核能力：默认 false，真值只来自 ContextAuditSection 的单次读取。
  const [auditSupported, setAuditSupported] = useState(false)
  const onAuditAvailability = useCallback((supported: boolean) => setAuditSupported(supported), [])
  // 运行时环境（Python/Node/Git）。状态在 Go 侧，这里只做它的镜子；只有设置页用到，
  // 所以钩子就挂在这里，不必像 update 那样提到 App（侧栏小红点要读那个）。
  const runtimes = useRuntimes()
  useEffect(() => {
    listCustomModels().then((l) => setCustomModels(l ?? [])).catch(() => {})
  }, [])

  const caps = useMemo(() => ({
    recorderEnabled: BRAND.features.recorder,
    auditSupported,
  }), [auditSupported])
  const categories = useMemo(() => buildSettingsNavigation(caps), [caps])
  const visibleIds = useMemo(() => visibleSectionIds(categories), [categories])
  const nav = useSettingsNavigation({ visibleIds })
  const navigateTo = nav.navigateTo

  // 入口指定了目标节时，子锚点 ref 已挂上后再滚过去（layout），避免停在目录顶。
  useLayoutEffect(() => {
    if (!initialSection || !visibleIds.includes(initialSection)) return
    navigateTo(initialSection)
  }, [initialSection, visibleIds, navigateTo])

  // switchOpts drives the live 模型 switcher: platform + custom together, since
  // picking either now switches the connection in place (via SwitchModel). modelOpts
  // stays platform-only for the harm-judge field, which takes a bare model id the
  // engine resolves — a custom profile's display name is not such an id.
  const switchOpts = toModelOptions(platformModels, customModels)
  const modelOpts = toModelOptions(platformModels, [])
  async function save() {
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      const i = await saveSettings({
        permissionMode,
        harmJudgeModel,
        harmJudgeVotes,
        skipLogin,
      })
      if (i && i.model) onSaved(i)
      setSaved(true)
      setTimeout(() => setSaved(false), 2200)
    } catch (e) {
      setError(errText(e))
    } finally {
      setSaving(false)
    }
  }

  const register = nav.registerSection
  const [accountCat, modelsCat, mediaCat, environmentCat, aboutCat] = SETTINGS_CATEGORIES

  return (
    <PageShell
      title="设置"
      hint="按目录定位设置。模型选择即时生效，其他项目按各区说明保存与生效。"
      width={864}
      scrollRef={nav.scrollRef}
      contentClassName="settings-shell"
    >
      <div ref={nav.layoutRef} className="settings-layout" data-narrow={nav.narrow ? 'true' : 'false'}>
        <SettingsNavigation
          categories={categories}
          activeId={nav.activeId}
          onNavigate={nav.navigateTo}
          narrow={nav.narrow}
          compactNavRef={nav.compactNavRef}
        />
        <div ref={nav.contentRef} className="settings-content">
          <SettingsGroup category={accountCat}>
            <SettingsAnchor section={sectionMeta('passport')} register={register}>
              <AccountSection
                onAccount={(tenantId, platform, ready) => {
                  setActiveTenantId(tenantId)
                  setPlatformModels(platform)
                  setAccountReady(ready)
                }}
                onBusy={setAccountBusy}
              />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('login')} register={register}>
              <Section title="登录" hint="保存后于下次启动/登出时生效">
                <label className="flex items-center justify-between gap-3 cursor-pointer">
                  <span className="min-w-0">
                    <span className="text-[13px]">免登录</span>
                    <span className="block text-[12px] text-muted mt-0.5">关闭（默认）时启动强制登录通行证；开启后未登录也可直接进入并使用本地自定义模型。</span>
                  </span>
                  <Toggle on={skipLogin} onChange={setSkipLogin} />
                </label>
              </Section>
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={modelsCat}>
            <SettingsAnchor section={sectionMeta('session')} register={register}>
              <div className="min-w-0">
                <SessionSection
                  currentModel={info?.model ?? ''}
                  switchOpts={switchOpts}
                  onSwitchModel={onSwitchModel}
                  busy={busy}
                  permissionMode={permissionMode}
                  onPermissionMode={setPermissionMode}
                  harmJudgeModel={harmJudgeModel}
                  onHarmJudgeModel={setHarmJudgeModel}
                  harmJudgeVotes={harmJudgeVotes}
                  onHarmJudgeVotes={setHarmJudgeVotes}
                  modelOpts={modelOpts}
                />
              </div>
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('custom-models')} register={register}>
              <CustomModelsSection models={customModels} onChanged={setCustomModels} />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('model-context')} register={register}>
              <ContextSection tenantId={activeTenantId} platform={platformModels} custom={customModels} disabled={accountBusy} />
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={mediaCat}>
            <SettingsAnchor section={sectionMeta('vision')} register={register}>
              <VisionSection tenantId={activeTenantId} platform={platformModels} custom={customModels} onPlatformChanged={setPlatformModels} />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('image-generation')} register={register}>
              <ImageGenerationSection tenantId={activeTenantId} />
            </SettingsAnchor>
            {/* 录音设置跟着「录音纪要」这个功能走：品牌关掉它时这块也不画，否则设置页
                里留着一组调不出任何界面的开关。 */}
            {BRAND.features.recorder && (
              <SettingsAnchor section={sectionMeta('recorder')} register={register}>
                <RecorderSection />
              </SettingsAnchor>
            )}
          </SettingsGroup>

          <SettingsGroup category={environmentCat}>
            <SettingsAnchor section={sectionMeta('proxy')} register={register}>
              <ProxySection />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('runtimes')} register={register}>
              <RuntimesSection runtimes={runtimes} />
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={aboutCat}>
            <SettingsAnchor section={sectionMeta('context-audit')} register={register} hidden={!auditSupported}>
              <ContextAuditSection onAvailabilityChange={onAuditAvailability} />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('updates')} register={register}>
              <AboutSection update={update} />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('workspace')} register={register}>
              <Section title="工作区">
                <div className="font-mono text-[13px] text-muted break-all">{info?.cwd || '—'}</div>
              </Section>
            </SettingsAnchor>
          </SettingsGroup>

          {error && <InlineError variant="text">{error}</InlineError>}
          <div className="flex flex-wrap items-center gap-3 pb-2">
            <button className={`${BTN} ${BTN_PRIMARY} px-7 py-2.5`} disabled={saving || accountBusy || (!!activeTenantId && !accountReady)} onClick={save}>{saving ? '保存中…' : accountBusy ? '正在同步租户…' : '保存设置'}</button>
            <span className="text-[12px] text-muted">仅保存免登录、权限模式与判定设置；其他项目在各自区域保存。</span>
            {saved && <span className="text-green text-[13px]">✓ 已保存</span>}
          </div>
        </div>
      </div>
    </PageShell>
  )
}
