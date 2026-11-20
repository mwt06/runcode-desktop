import { describe, expect, it } from 'vitest'
import {
  SETTINGS_CATEGORIES,
  SETTINGS_NARROW_BREAKPOINT,
  SETTINGS_SECTION_ORDER,
  buildSettingsNavigation,
  isNarrowLayout,
  resolveActiveSection,
  resolveVisibleSection,
  sectionAnchorId,
  visibleSectionIds,
  type SettingsCapabilities,
  type SettingsSectionId,
} from './navigation-model'

const ALL_IDS: SettingsSectionId[] = [
  'passport', 'login',
  'session', 'custom-models', 'model-context',
  'vision', 'image-generation', 'recorder',
  'proxy', 'runtimes',
  'context-audit', 'updates', 'workspace',
]

const ids = (caps: SettingsCapabilities) => visibleSectionIds(buildSettingsNavigation(caps))

describe('settings catalog', () => {
  it('listsFiveCategoriesInFixedOrder', () => {
    expect(SETTINGS_CATEGORIES.map((c) => c.id)).toEqual(['account', 'models', 'media', 'environment', 'about'])
    expect(SETTINGS_CATEGORIES.map((c) => c.title)).toEqual(['账号与登录', '模型与对话', '图片与录音', '网络与运行环境', '关于与诊断'])
  })

  it('placesEachOfThirteenSectionsExactlyOnce', () => {
    const flat = SETTINGS_CATEGORIES.flatMap((c) => c.sections.map((s) => s.id))
    expect(flat).toEqual(ALL_IDS)
    expect(new Set(flat).size).toBe(13)
    expect(SETTINGS_SECTION_ORDER).toEqual(ALL_IDS)
    const anchors = flat.map(sectionAnchorId)
    expect(new Set(anchors).size).toBe(13)
    expect(anchors[0]).toBe('settings-passport')
  })

  it('assignsSectionsToTheirCategories', () => {
    const owner = Object.fromEntries(SETTINGS_CATEGORIES.flatMap((c) => c.sections.map((s) => [s.id, c.id])))
    expect(owner).toEqual({
      passport: 'account', login: 'account',
      session: 'models', 'custom-models': 'models', 'model-context': 'models',
      vision: 'media', 'image-generation': 'media', recorder: 'media',
      proxy: 'environment', runtimes: 'environment',
      'context-audit': 'about', updates: 'about', workspace: 'about',
    })
  })

  it('keepsImageGenerationBetweenVisionAndRecorderRegardlessOfCapabilities', () => {
    const media = SETTINGS_CATEGORIES.find((c) => c.id === 'media')!
    expect(media.sections.map((s) => s.id)).toEqual(['vision', 'image-generation', 'recorder'])
    expect(media.sections.find((s) => s.id === 'image-generation')!.title).toBe('图片生成')
    for (const recorderEnabled of [false, true]) {
      for (const auditSupported of [false, true]) {
        expect(ids({ recorderEnabled, auditSupported })).toContain('image-generation')
      }
    }
  })

  it('countsVisibleSectionsPerCapabilityCombination', () => {
    expect(ids({ recorderEnabled: false, auditSupported: true })).toHaveLength(12)
    expect(ids({ recorderEnabled: false, auditSupported: false })).toHaveLength(11)
    expect(ids({ recorderEnabled: true, auditSupported: true })).toHaveLength(13)
    expect(ids({ recorderEnabled: true, auditSupported: false })).toHaveLength(12)
    expect(ids({ recorderEnabled: false, auditSupported: false })).not.toContain('recorder')
    expect(ids({ recorderEnabled: false, auditSupported: false })).not.toContain('context-audit')
  })

  it('neverDropsACategoryBecauseItsConditionalSectionIsHidden', () => {
    const nav = buildSettingsNavigation({ recorderEnabled: false, auditSupported: false })
    expect(nav.map((c) => c.id)).toEqual(['account', 'models', 'media', 'environment', 'about'])
    expect(nav.find((c) => c.id === 'media')!.sections.map((s) => s.id)).toEqual(['vision', 'image-generation'])
    expect(nav.find((c) => c.id === 'about')!.sections.map((s) => s.id)).toEqual(['updates', 'workspace'])
  })

  it('doesNotMutateTheFullCatalog', () => {
    buildSettingsNavigation({ recorderEnabled: false, auditSupported: false })
    expect(SETTINGS_CATEGORIES.flatMap((c) => c.sections)).toHaveLength(13)
  })
})

describe('resolveVisibleSection', () => {
  const all = ids({ recorderEnabled: true, auditSupported: true })

  it('keepsTheCurrentSectionWhileVisible', () => {
    expect(resolveVisibleSection('context-audit', all)).toBe('context-audit')
  })

  it('fallsBackToUpdatesWhenAuditDisappears', () => {
    expect(resolveVisibleSection('context-audit', ids({ recorderEnabled: true, auditSupported: false }))).toBe('updates')
  })

  it('fallsBackToProxyWhenRecorderDisappears', () => {
    expect(resolveVisibleSection('recorder', ids({ recorderEnabled: false, auditSupported: true }))).toBe('proxy')
  })

  it('fallsBackToThePreviousSectionWithoutSuccessor', () => {
    expect(resolveVisibleSection('workspace', ['passport', 'login', 'updates'])).toBe('updates')
  })

  it('returnsNullForAnEmptyList', () => {
    expect(resolveVisibleSection('passport', [])).toBeNull()
  })
})

describe('resolveActiveSection', () => {
  const positions = [
    { id: 'passport' as const, top: 30 },
    { id: 'login' as const, top: 260 },
    { id: 'session' as const, top: 520 },
    { id: 'workspace' as const, top: 900 },
  ]

  it('picksTheFirstSectionBeforeAnyCrossesTheLine', () => {
    expect(resolveActiveSection(positions, 24, false)).toBe('passport')
  })

  it('picksTheLastSectionAtOrAboveTheLine', () => {
    expect(resolveActiveSection(positions.map((p) => ({ ...p, top: p.top - 260 + 24 })), 24, false)).toBe('login')
    expect(resolveActiveSection(positions.map((p) => ({ ...p, top: p.top - 400 })), 24, false)).toBe('login')
    expect(resolveActiveSection(positions.map((p) => ({ ...p, top: p.top - 600 })), 24, false)).toBe('session')
  })

  it('picksTheLastSectionAtTheBottomEvenIfItNeverCrossesTheLine', () => {
    expect(resolveActiveSection(positions.map((p) => ({ ...p, top: p.top - 600 })), 24, true)).toBe('workspace')
  })

  it('returnsNullWithoutPositions', () => {
    expect(resolveActiveSection([], 24, false)).toBeNull()
    expect(resolveActiveSection([], 24, true)).toBeNull()
  })
})

describe('isNarrowLayout', () => {
  it('switchesBelow864CssPixels', () => {
    expect(SETTINGS_NARROW_BREAKPOINT).toBe(864)
    expect(isNarrowLayout(863)).toBe(true)
    expect(isNarrowLayout(864)).toBe(false)
    expect(isNarrowLayout(865)).toBe(false)
  })

  it('treatsAnUnmeasuredLayoutAsUnknown', () => {
    expect(isNarrowLayout(0)).toBeNull()
  })
})
