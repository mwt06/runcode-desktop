// 设置页目录：五个大类、十三个小节的固定清单，以及目录要用的纯函数。
//
// 清单只放 ID、标题与顺序，不放组件或账号对象——正文按完整清单挂载、目录按可见
// 清单显示，二者靠同一份 ID 对上。ID 是写死的英文，不从数组下标或中文标题推导：
// 标题改个字、顺序挪一格，都不该让锚点、组件 key 或测试跟着漂移。
export type SettingsCategoryId = 'account' | 'models' | 'media' | 'environment' | 'about'
export type SettingsSectionId = 'passport' | 'login' | 'session' | 'custom-models'
  | 'model-context' | 'vision' | 'image-generation' | 'recorder' | 'proxy' | 'runtimes'
  | 'context-audit' | 'updates' | 'workspace'

// 只有这两项决定小节显隐。图片生成刻意不在这里：它的可用模型随账号、租户与目录
// 异步变化，据此隐藏会让目录随切租户闪动，还会把"暂无可用模型""目录加载失败"这类
// 用户必须看到的空态一并藏掉——那些状态在卡片里显示，目录项常驻。
export type SettingsCapabilities = { recorderEnabled: boolean; auditSupported: boolean }
export type SettingsSection = { id: SettingsSectionId; title: string }
export type SettingsCategory = { id: SettingsCategoryId; title: string; sections: readonly SettingsSection[] }
export type SectionPosition = { id: SettingsSectionId; top: number }

export const SETTINGS_CATEGORIES: readonly SettingsCategory[] = [
  { id: 'account', title: '账号与登录', sections: [
    { id: 'passport', title: '账号（通行证）' },
    { id: 'login', title: '登录' },
  ] },
  { id: 'models', title: '模型与对话', sections: [
    { id: 'session', title: '会话' },
    { id: 'custom-models', title: '自定义模型' },
    { id: 'model-context', title: '模型上下文配置' },
  ] },
  { id: 'media', title: '图片与录音', sections: [
    { id: 'vision', title: '图片识别' },
    { id: 'image-generation', title: '图片生成' },
    { id: 'recorder', title: '录音纪要' },
  ] },
  { id: 'environment', title: '网络与运行环境', sections: [
    { id: 'proxy', title: '出网代理' },
    { id: 'runtimes', title: '运行时环境' },
  ] },
  { id: 'about', title: '关于与诊断', sections: [
    { id: 'context-audit', title: '上下文审核' },
    { id: 'updates', title: '关于与更新' },
    { id: 'workspace', title: '工作区' },
  ] },
]

export const SETTINGS_SECTION_ORDER: readonly SettingsSectionId[] =
  SETTINGS_CATEGORIES.flatMap((c) => c.sections.map((s) => s.id))

// 可用内容宽度低于它改单列 + 顶部目录；正好 864 仍是双列（200 目录 + 24 间距 + 640 正文）。
export const SETTINGS_NARROW_BREAKPOINT = 864

export function sectionAnchorId(id: SettingsSectionId): string {
  return `settings-${id}`
}

function sectionVisible(id: SettingsSectionId, caps: SettingsCapabilities): boolean {
  if (id === 'recorder') return caps.recorderEnabled
  if (id === 'context-audit') return caps.auditSupported
  return true
}

// 按能力过滤后的目录。没有可见小节的大类整类去掉，不留空标题；按现有清单每类都有
// 常驻小节，所以这只是兜底。
export function buildSettingsNavigation(caps: SettingsCapabilities): SettingsCategory[] {
  return SETTINGS_CATEGORIES
    .map((c) => ({ ...c, sections: c.sections.filter((s) => sectionVisible(s.id, caps)) }))
    .filter((c) => c.sections.length > 0)
}

export function visibleSectionIds(categories: readonly SettingsCategory[]): SettingsSectionId[] {
  return categories.flatMap((c) => c.sections.map((s) => s.id))
}

// 当前项被隐藏时的回落：按完整清单顺序取它之后第一个可见项，没有后继取前一个可见
// 项；仍可见则保持；可见列表为空返回 null。
export function resolveVisibleSection(
  current: SettingsSectionId,
  visible: readonly SettingsSectionId[],
): SettingsSectionId | null {
  if (visible.length === 0) return null
  if (visible.includes(current)) return current
  const at = SETTINGS_SECTION_ORDER.indexOf(current)
  for (let i = at + 1; i < SETTINGS_SECTION_ORDER.length; i++) {
    if (visible.includes(SETTINGS_SECTION_ORDER[i])) return SETTINGS_SECTION_ORDER[i]
  }
  for (let i = at - 1; i >= 0; i--) {
    if (visible.includes(SETTINGS_SECTION_ORDER[i])) return SETTINGS_SECTION_ORDER[i]
  }
  return visible[0]
}

// 滚动位置 → 当前项。positions 按文档顺序，top 是标题相对滚动视口顶边的距离。
// 取最后一个越过定位线（top ≤ topInsetPx）的小节，都没越过取第一个；到底时取最后
// 一个——页尾的短卡片（工作区）永远越不过定位线，没有这条就永远选不中它。
export function resolveActiveSection(
  positions: readonly SectionPosition[],
  topInsetPx: number,
  atBottom: boolean,
): SettingsSectionId | null {
  if (positions.length === 0) return null
  if (atBottom) return positions[positions.length - 1].id
  let active = positions[0].id
  for (const p of positions) {
    if (p.top <= topInsetPx) active = p.id
    else break
  }
  return active
}

// 0 宽说明还没排版（未挂进文档、jsdom），此时不下结论，由调用方保持原布局。
export function isNarrowLayout(contentWidth: number): boolean | null {
  if (!(contentWidth > 0)) return null
  return contentWidth < SETTINGS_NARROW_BREAKPOINT
}
