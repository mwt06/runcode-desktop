import { applyScenario, skillHint, type Scenario } from '@/core/scenarios'
import type { SkillList, SkillMarketPage } from '@/core/bridge'

export interface ScenarioActions {
  list: () => Promise<SkillList>
  market: () => Promise<SkillMarketPage>
  install: (id: number) => Promise<unknown>
  installing: (name: string | null) => void
  notify: (message: string) => void
  errorText: (error: unknown) => string
}

// 安装失败不妨碍使用场景本身；只是不再点名不可用的技能。
export async function ensureScenarioSkill(name: string, deps: ScenarioActions): Promise<boolean> {
  const local = await deps.list().catch(() => null)
  if ((local?.skills ?? []).some((s) => s.name === name && !s.disabledUser && !s.disabledProject)) return true
  // 已安装但停用的技能不应该被场景选择悄悄重新启用。
  if ((local?.skills ?? []).some((s) => s.name === name)) return false
  deps.installing(name)
  try {
    const page = await deps.market()
    const hit = (page.skills ?? []).find((s) => s.name === name)
    if (!hit) {
      deps.notify(`市场里没有「${name}」技能，这次先不带它跑`)
      return false
    }
    await deps.install(hit.id)
    deps.notify(`已安装「${name}」技能`)
    return true
  } catch (e) {
    deps.notify(deps.errorText(e))
    return false
  } finally {
    deps.installing(null)
  }
}

// 异步返回后检查归属，草稿现读，不能覆盖等待期间的新输入。
export async function pickScenarioAction(sc: Scenario, deps: ScenarioActions, target: {
  current: () => boolean
  read: () => string
  apply: (selection: ReturnType<typeof applyScenario>) => void
}) {
  const ready = sc.skill ? await ensureScenarioSkill(sc.skill, deps) : false
  if (!target.current()) return
  target.apply(applyScenario(target.read(), skillHint(ready ? sc.skill : '') + sc.prompt))
}
