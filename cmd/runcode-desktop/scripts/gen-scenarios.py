# -*- coding: utf-8 -*-
"""把产品侧的《内置场景》表导成 frontend/src/core/scenarios.ts。

产物已提交进仓库，**运行期和构建期都不读 Excel**——桌面构建链路里没有 Python，
把 xlsx 变成构建输入等于给每个打包的人装一套 Python 环境。表更新了在本机跑一次、
把生成的 .ts 一起提交即可：

    pip install openpyxl
    python scripts/gen-scenarios.py

只导「默认场景 = 是/新增」的那些行。分类 id 与图标是下面手工定死的映射：中文分类名
当不了 id（要进 React key，将来还要和网桥 /api/scenarios 的清单对齐）。
"""
import sys, re, json, io
import openpyxl

sys.stdout.reconfigure(encoding='utf-8', errors='replace')

XLSX = "../../内置场景_v2 （全部）（8.25）终.xlsx"
OUT = "frontend/src/core/scenarios.ts"

# 分类 id 与图标：中文分类名不能当 id（要进 React key、将来还要和网桥的清单对齐），
# 这里手工定死一份映射。图标只用 ui/icons 里已有的名字。
CATS = [
    ('OA查询',  'oa',          'search'),
    ('帮我写作', 'writing',     'pencil'),
    ('录音纪要', 'recorder',    'mic'),
    ('调研研究', 'research',    'compass'),
    ('格式校验', 'format',      'file-edit'),
    ('幻灯片',  'slides',      'file-ppt'),
    ('教育教学', 'teaching',    'book'),
    ('思维工具', 'thinking',    'sparkles'),
    ('创意图文', 'visual',      'file-image'),
    ('数据分析', 'data',        'grid'),
    ('软件联通', 'integration', 'plug'),
]
CAT_ID = {c: i for c, i, _ in CATS}
CAT_ICON = {c: ic for c, _, ic in CATS}
# 录音纪要不是 skill，是客户端内置功能（本地转写→纪要）。它由 App 直接接管。
BUILTIN = {'recorder': 'recorder'}

NAME = re.compile(r'^[A-Za-z0-9_-]{1,64}$')

wb = openpyxl.load_workbook(XLSX, data_only=True)
ws = wb['Sheet1']
rows, scene = [], ''
for i, r in enumerate(ws.iter_rows(values_only=True), 1):
    if i == 1:
        continue
    v = [('' if c is None else str(c).replace('\n', ' ').strip()) for c in r]
    if not any(v):
        continue
    if v[0]:
        scene = v[0]
    rows.append(dict(scene=scene, sub=v[1], dflt=v[2], prompt=v[3], desc=v[4], skill=v[5]))

# 合并单元格的写法：同一分类里后续行的描述/关联留空 = 沿用本分类第一行的。
carry = {}
for r in rows:
    if r['desc']:
        carry[r['scene']] = (r['desc'], r['skill'])
    elif r['scene'] in carry:
        r['desc'], r['skill'] = carry[r['scene']]

defaults = [r for r in rows if r['dflt'] in ('是', '新增')]

groups = []
for cname, cid, icon in CATS:
    items = [r for r in defaults if r['scene'] == cname]
    if not items:
        continue
    out = []
    for n, r in enumerate(items, 1):
        skill = r['skill'] if NAME.match(r['skill']) else ''
        out.append(dict(
            id=skill or ('%s-%d' % (cid, n)),
            name=r['sub'],
            prompt=r['prompt'],
            blurb=r['desc'],
            skill=skill,
        ))
    groups.append(dict(id=cid, name=cname, icon=icon, items=out))

j = lambda s: json.dumps(s, ensure_ascii=False)

L = []
L.append('''// 内置场景：对话栏上方那排按分类分级选的常用任务。
//
// 数据来自产品侧的《内置场景》表（v2 8.25 终版）里「默认场景 = 是/新增」的那 45 行，
// 由 scripts 一次性导出后提交进来——**运行期不读 Excel**，这里就是唯一事实来源。
// 表更新了重新导一次，别手改：手改会和下一次导出打架。
//
// 为什么内置而不是拉服务端：起始页与输入框上方是首屏，未登录、离线、网关抖动时
// 都得画得出来。将来网桥提供 /api/scenarios 时按「内置一份兜底 + 拉到就覆盖」接，
// 与 syncMarketOnce / SkillMarket 的缓存回退是同一套路——所以 skill 字段现在就带着，
// 虽然本阶段用不到（见下）。

/** Scenario 是一条可以一键填进输入框的常用任务。 */
export interface Scenario {
  /** 稳定主键。有关联 skill 的直接用 skill 名，其余是 <分类 id>-<序号>。 */
  id: string
  name: string
  /** 默认提示词。含【】占位符，点选后由调用方选中第一个（见 firstPlaceholder）。 */
  prompt: string
  /** 一句话说明这个场景干什么，选择面板里显示。 */
  blurb: string
  /**
   * 关联的市场技能 id；没有关联的为空（OA 那三条走 MCP，录音纪要是内置功能）。
   *
   * 本阶段**不用**它：提示词本身就是完整可用的，装了对应技能只是效果更好，而技能
   * 该不该加载由引擎按 frontmatter 的 description 自行判断——这正是那一句的用途。
   * 留着它是为了下一阶段「未装则一键从市场装」不必再改一次数据。
   */
  skill: string
}

/** ScenarioCategory 是一级分类。 */
export interface ScenarioCategory {
  id: string
  name: string
  /** ui/icons 里的图标名。 */
  icon: string
  items: Scenario[]
}
''')

L.append('export const SCENARIOS: ScenarioCategory[] = [')
for g in groups:
    L.append('  {')
    L.append('    id: %s, name: %s, icon: %s,' % (j(g['id']), j(g['name']), j(g['icon'])))
    L.append('    items: [')
    for it in g['items']:
        L.append('      {')
        L.append('        id: %s,' % j(it['id']))
        L.append('        name: %s,' % j(it['name']))
        L.append('        prompt: %s,' % j(it['prompt']))
        L.append('        blurb: %s,' % j(it['blurb']))
        L.append('        skill: %s,' % j(it['skill']))
        L.append('      },')
    L.append('    ],')
    L.append('  },')
L.append(']')
L.append('')

L.append('''/**
 * skillHint 是「先用这个技能」那句话，拼在场景提示词前面。
 *
 * 为什么要显式点名，而不是靠引擎自己判断：引擎按 frontmatter 的 description 决定
 * 加不加载某个技能，那是**面向全部技能的一次匹配**，装了几十个之后未必挑中你想要
 * 的那一个。而场景与技能的对应关系是产品侧一条条定死的——已经知道答案了，就别让
 * 模型再猜一次。
 *
 * 只在技能**确实可用**时才拼（见 App 的 pickScenario）：点名一个不存在的技能，
 * 模型会去调 Skill 工具然后报一句找不到，比不提它还糟。
 */
export function skillHint(skill: string): string {
  return skill ? `请使用「${skill}」技能完成以下任务：
` : ''
}

/**
 * BUILTIN_CATEGORY 把分类映射到「它其实是客户端的一个内置功能」。
 *
 * 录音纪要就一条，而且不是提示词——录音是点一下就该开始的动作，让用户去输入框里
 * 打字触发不合适（这也是它原先单独摆在这排里的原因）。所以这个分类不展开二级面板，
 * 点了直接执行，由 App 把动作接进来。
 */
export const BUILTIN_CATEGORY: Record<string, string> = { recorder: 'recorder' }

/**
 * firstPlaceholder 找出提示词里第一个【占位符】的位置（含书名号本身）。
 *
 * 点了场景要把提示词填进输入框并**选中**这一段，用户接着打字就替换掉它。直接发送
 * 是不行的：模型收到「帮我调研【XX话题】」只能反问，而那一问一答本可以省掉。
 *
 * 返回 null 表示这条提示词没有占位符（表里有 7 条是这样，比如「帮我做人生设计」），
 * 调用方把光标放到末尾即可。
 */
export function firstPlaceholder(prompt: string): { start: number; end: number } | null {
  const start = prompt.indexOf('\\u3010')
  if (start < 0) return null
  const end = prompt.indexOf('\\u3011', start)
  if (end < 0) return null
  return { start, end: end + 1 }
}

/**
 * applyScenario 算出「点了这条场景之后输入框该是什么、选中哪一段」。
 *
 * 输入框空着就整段替换；已经有字了则**另起一段追加**，不覆盖用户已经打的内容——
 * 场景提示词是一整段模板，直接顶掉别人写了一半的话，损失比省下的一次点击大得多。
 */
export function applyScenario(current: string, prompt: string): { value: string; start: number; end: number } {
  const prefix = current.trim() ? current.replace(/\\s+$/, '') + '\\n\\n' : ''
  const value = prefix + prompt
  const ph = firstPlaceholder(prompt)
  return ph
    ? { value, start: prefix.length + ph.start, end: prefix.length + ph.end }
    : { value, start: value.length, end: value.length }
}
''')

io.open(OUT, 'w', encoding='utf-8', newline='\n').write('\n'.join(L))
print('wrote %s' % OUT)
print('分类 %d，场景 %d' % (len(groups), sum(len(g['items']) for g in groups)))
for g in groups:
    print('  %-12s %-6s %d' % (g['id'], g['name'], len(g['items'])))
