// 设置页：按小节拆分——账号(通行证)、会话、自定义模型、联网工具代理、上下文长度。
// 拆分原则——进 saveSettings() 载荷的字段（会话）受控于 SettingsPage 本体；
// 各自直接落后端的小节（账号/代理/自定义模型草稿）状态内化，只把共享数据（平台
// 模型、自定义模型列表，二者合成模型候选）回流给父级。
//
// 分类标题切换右侧完整分类，三级项常驻且只改变滚动与焦点；不保存、不重建账号
// 协调器、不改变业务小节的 key。正文按完整清单顺序挂载，目录按可见清单过滤。
// 录音按品牌决定是否挂载；审核探测始终挂载，未知／不支持只隐藏包装与目录项。
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { toModelOptions, type ModelOption } from '@/ui/model-picker'
import {
  listCustomModels, saveSettings,
  type CustomModel, type PassportModel, type SessionInfo, type SettingsView,
} from '@/core/bridge'
import { Toggle } from '@/ui/toggle'
import { BRAND } from '@/core/brand'
import { Section } from './section'
import { AccountSection } from './account'
import { MailSection } from './mail'
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
import { type UpdateController } from '@/session/use-update'
import { useRuntimes } from '@/session/use-runtimes'
import {
  SETTINGS_CATEGORIES,
  buildSettingsNavigation,
  categoryForSection,
  sectionsInCategory,
  visibleSectionIds,
  type SettingsCategoryId,
  type SettingsSectionId,
} from './navigation-model'
import { SettingsAnchor, SettingsGroup, SettingsNavigation } from './navigation'
import { useSettingsNavigation } from './use-settings-navigation'
import { createAutoSaveChannel, type AutoSaveChannel, type AutoSaveSnapshot } from './autosave-channel'
import { useSettingsSaveRegistry } from './settings-save-provider'

function sectionMeta(id: SettingsSectionId) {
  for (const category of SETTINGS_CATEGORIES) {
    const section = category.sections.find((s) => s.id === id)
    if (section) return section
  }
  throw new Error(`unknown settings section: ${id}`)
}

type FourFieldDraft = {
  permissionMode: string
  harmJudgeModel: string
  harmJudgeVotes: number
  skipLogin: boolean
}

function AutoSaveStatus({ snapshot }: { snapshot: AutoSaveSnapshot<FourFieldDraft> }) {
  const text = snapshot.phase === 'saving' ? '正在自动保存…'
    : snapshot.phase === 'saved' ? '已自动保存'
      : snapshot.phase === 'invalid' || snapshot.phase === 'error' ? `未保存：${snapshot.error}`
        : ''
  if (!text) return null
  return (
    <p
      role="status"
      aria-live="polite"
      className={`text-[12px] ${snapshot.phase === 'invalid' || snapshot.phase === 'error' ? 'text-red' : 'text-muted'}`}
    >
      {text}
    </p>
  )
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
  const [accountBusy, setAccountBusy] = useState(false)
  // 共享数据：平台模型（账号小节回流）+ 自定义模型列表（自定义模型小节维护），
  // 合成会话/判定模型选择器共用的候选列表。
  const [platformModels, setPlatformModels] = useState<PassportModel[]>([])
  const [activeTenantId, setActiveTenantId] = useState(initial.tenantId ?? '')
  const [accountReady, setAccountReady] = useState(false)
  const [customModels, setCustomModels] = useState<CustomModel[]>([])
  const settingsSaves = useSettingsSaveRegistry()
  const onSavedRef = useRef(onSaved)
  onSavedRef.current = onSaved
  const settingsSessionId = info?.sessionId ?? ''
  const authoritativeFour = useMemo<FourFieldDraft>(() => ({
    permissionMode: info?.permissionMode || initial.permissionMode || 'interactive',
    harmJudgeModel: initial.harmJudgeModel ?? '',
    harmJudgeVotes: initial.harmJudgeVotes ?? 1,
    skipLogin: initial.skipLogin ?? false,
  }), [info?.permissionMode, initial.harmJudgeModel, initial.harmJudgeVotes, initial.permissionMode, initial.skipLogin])
  const fourFieldKey = `four:${settingsSessionId}:${activeTenantId}`
  const fourChannelRef = useRef<{
    key: string
    channel: AutoSaveChannel<FourFieldDraft>
  } | null>(null)
  if (!fourChannelRef.current || fourChannelRef.current.key !== fourFieldKey) {
    const existing = settingsSaves.getChannel<FourFieldDraft>(fourFieldKey)
    const channel = existing ?? createAutoSaveChannel<FourFieldDraft>({
      initial: authoritativeFour,
      delayMs: 600,
      write: async (value) => {
        const saved = await saveSettings({
          sessionId: settingsSessionId,
          permissionMode: value.permissionMode,
          harmJudgeModel: value.harmJudgeModel,
          harmJudgeVotes: value.harmJudgeVotes,
          skipLogin: value.skipLogin,
        })
        if (saved?.model) onSavedRef.current(saved)
        return value
      },
    })
    settingsSaves.register(fourFieldKey, '登录与会话设置', channel)
    fourChannelRef.current = { key: fourFieldKey, channel }
  }
  const fourChannel = fourChannelRef.current.channel
  const four = useSyncExternalStore(fourChannel.subscribe, fourChannel.getSnapshot, fourChannel.getSnapshot)
  const { permissionMode, harmJudgeModel, harmJudgeVotes, skipLogin } = four.draft
  const [selectedCategoryId, setSelectedCategoryId] = useState<SettingsCategoryId>(
    initialSection ? categoryForSection(initialSection).id : 'account',
  )
  const [pendingTarget, setPendingTarget] = useState<SettingsSectionId | null>(null)
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
  const selectedCategory = categories.find((category) => category.id === selectedCategoryId) ?? categories[0]
  const displayedIds = useMemo(
    () => selectedCategory ? sectionsInCategory(categories, selectedCategory.id) : [],
    [categories, selectedCategory],
  )
  const nav = useSettingsNavigation({ availableIds: visibleIds, displayedIds })
  const navigateTo = nav.navigateTo
  const initialConsumed = useRef(false)

  // 入口目标只消费一次；审核能力等异步更新不能把已手动切组的用户再拉回入口。
  useLayoutEffect(() => {
    if (!initialSection || initialConsumed.current) return
    initialConsumed.current = true
    if (!visibleIds.includes(initialSection)) return
    const categoryId = categoryForSection(initialSection).id
    setSelectedCategoryId(categoryId)
    setPendingTarget(initialSection)
  }, [initialSection, visibleIds])

  // 跨组导航必须等目标组提交 DOM 后再测量，隐藏元素的零几何不能参与定位。
  useLayoutEffect(() => {
    if (!pendingTarget || !displayedIds.includes(pendingTarget)) return
    navigateTo(pendingTarget)
    setPendingTarget(null)
  }, [displayedIds, navigateTo, pendingTarget])

  const navigateSection = useCallback((id: SettingsSectionId) => {
    const categoryId = categoryForSection(id).id
    setSelectedCategoryId(categoryId)
    setPendingTarget(id)
  }, [])

  useEffect(() => {
    if (categories.some((category) => category.id === selectedCategoryId)) return
    const fallback = nav.activeId ? categoryForSection(nav.activeId).id : categories[0]?.id
    setSelectedCategoryId(fallback ?? 'account')
  }, [categories, nav.activeId, selectedCategoryId])

  // 外部会话/配置变化只刷新未编辑字段，用户刚输入的内容不会被迟到回读覆盖。
  useEffect(() => {
    fourChannel.rebase(authoritativeFour)
  }, [authoritativeFour, fourChannel])
  useEffect(() => {
    fourChannel.setReady(!accountBusy && (!activeTenantId || accountReady))
  }, [accountBusy, accountReady, activeTenantId, fourChannel])
  const editFour = (patch: Partial<FourFieldDraft>) => {
    // 同一事件循环里连续改两个字段时，以通道中的最新草稿为基线，避免后一次覆盖前一次。
    fourChannel.edit({ ...fourChannel.getSnapshot().draft, ...patch }, 'immediate')
  }

  // switchOpts drives the live 模型 switcher: platform + custom together, since
  // picking either now switches the connection in place (via SwitchModel). modelOpts
  // stays platform-only for the harm-judge field, which takes a bare model id the
  // engine resolves — a custom profile's display name is not such an id.
  const switchOpts = toModelOptions(platformModels, customModels)
  const modelOpts = toModelOptions(platformModels, [])
  const register = nav.registerSection
  const [accountCat, modelsCat, mediaCat, environmentCat, aboutCat] = SETTINGS_CATEGORIES

  return (
    <div ref={nav.layoutRef} className="settings-layout" data-narrow={nav.narrow ? 'true' : 'false'}>
      {!nav.narrow && (
        <div className="settings-nav-column">
          <h2 className="settings-nav-title">设置</h2>
          <SettingsNavigation
            categories={categories}
            activeId={nav.activeId}
            onNavigate={navigateSection}
            narrow={false}
            compactNavRef={nav.compactNavRef}
          />
        </div>
      )}
      <div ref={nav.scrollRef} className="settings-content-scroll">
        {nav.narrow && (
          <SettingsNavigation
            categories={categories}
            activeId={nav.activeId}
            onNavigate={navigateSection}
            narrow
            compactNavRef={nav.compactNavRef}
          />
        )}
        <div ref={nav.contentRef} className="settings-content">
          {selectedCategory && (
            <header className="settings-page-heading">
              <h1>{selectedCategory.title}</h1>
              <p>{selectedCategory.description}</p>
            </header>
          )}
          <SettingsGroup category={accountCat} hidden={selectedCategoryId !== 'account'}>
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
              <Section title="登录" hint="自动保存 · 下次启动/登出时生效">
                <label className="flex items-center justify-between gap-3 cursor-pointer">
                  <span className="min-w-0">
                    <span className="text-[13px]">免登录</span>
                    <span className="block text-[12px] text-muted mt-0.5">关闭（默认）时启动强制登录通行证；开启后未登录也可直接进入并使用本地自定义模型。</span>
                  </span>
                  <Toggle on={skipLogin} onChange={(next) => editFour({ skipLogin: next })} />
                </label>
                <AutoSaveStatus snapshot={four} />
              </Section>
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('mail')} register={register}>
              <MailSection />
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={modelsCat} hidden={selectedCategoryId !== 'models'}>
            <SettingsAnchor section={sectionMeta('session')} register={register}>
              <div className="min-w-0">
                <SessionSection
                  currentModel={info?.model ?? ''}
                  switchOpts={switchOpts}
                  onSwitchModel={onSwitchModel}
                  busy={busy}
                  permissionMode={permissionMode}
                  onPermissionMode={(next) => editFour({ permissionMode: next })}
                  harmJudgeModel={harmJudgeModel}
                  onHarmJudgeModel={(next) => editFour({ harmJudgeModel: next })}
                  harmJudgeVotes={harmJudgeVotes}
                  onHarmJudgeVotes={(next) => editFour({ harmJudgeVotes: next })}
                  modelOpts={modelOpts}
                />
                <AutoSaveStatus snapshot={four} />
              </div>
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('custom-models')} register={register}>
              <CustomModelsSection models={customModels} onChanged={setCustomModels} />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('model-context')} register={register}>
              <ContextSection tenantId={activeTenantId} platform={platformModels} custom={customModels} disabled={accountBusy} />
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={mediaCat} hidden={selectedCategoryId !== 'media'}>
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

          <SettingsGroup category={environmentCat} hidden={selectedCategoryId !== 'environment'}>
            <SettingsAnchor section={sectionMeta('proxy')} register={register}>
              <ProxySection />
            </SettingsAnchor>
            <SettingsAnchor section={sectionMeta('runtimes')} register={register}>
              <RuntimesSection runtimes={runtimes} />
            </SettingsAnchor>
          </SettingsGroup>

          <SettingsGroup category={aboutCat} hidden={selectedCategoryId !== 'about'}>
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

        </div>
      </div>
    </div>
  )
}
