// 品牌场景目录是公共目录的一层选择，不改写由产品表生成的 SCENARIOS。
import { SCENARIOS, type ScenarioCategory } from './scenarios'

export type ScenarioProfile = 'full' | 'guokai'

const GUOKAI_OA: ScenarioCategory = {
  id: 'oa',
  name: 'OA信息查询',
  icon: 'search',
  items: [
    {
      id: 'oa-1', name: '待办事项查询',
      blurb: '查询OA系统里的最近待办事项',
      prompt: '帮我查询OA系统里我最近的待办事项',
      skill: '',
    },
    {
      id: 'oa-workflow', name: '办事流程查询',
      blurb: '查询OA系统里的某个办事的具体流程进展',
      prompt: '帮我查询OA系统里【办事名称】的办事具体流程进展，看看我当前需要做什么',
      skill: '',
    },
    {
      id: 'oa-3', name: '最新通知查询',
      blurb: '查询OA系统里的最近通知',
      prompt: '帮我查询OA系统里最近的通知，看看哪些值得我关注',
      skill: '',
    },
    {
      id: 'oa-2', name: '教职工名片信息查询',
      blurb: '查询OA系统里的教职工名片信息',
      prompt: '帮我查询OA系统里【人名】的个人名片信息',
      skill: '',
    },
    {
      id: 'oa-appointments', name: '干部任职查询',
      blurb: '查询OA系统里的干部任职信息',
      prompt: '帮我查询OA系统里最近的干部任职信息',
      skill: '',
    },
    {
      id: 'oa-regulations', name: '规章制度查询',
      blurb: '查询OA系统里的规章制度信息',
      prompt: '帮我查询OA系统里最近的规章制度，看看哪些值得我关注',
      skill: '',
    },
    {
      id: 'oa-party', name: '党群园地信息查询',
      blurb: '查询OA系统里的党群园地信息',
      prompt: '帮我查询OA系统里最近的党群园地信息',
      skill: '',
    },
  ],
}

// 顺序由版本决定；格式校验与幻灯片复用原条目及技能关联，不维护第二份数据。
const GUOKAI_SCENARIOS: ScenarioCategory[] = [
  GUOKAI_OA,
  ...['format', 'slides'].flatMap((id) => SCENARIOS.filter((category) => category.id === id)),
]

export function scenariosForProfile(profile: ScenarioProfile): ScenarioCategory[] {
  return profile === 'guokai' ? GUOKAI_SCENARIOS : SCENARIOS
}
