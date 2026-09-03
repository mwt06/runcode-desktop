import { describe, expect, it } from 'vitest'
import { BUILTIN_CATEGORY, SCENARIOS, applyScenario, firstPlaceholder, skillHint } from './scenarios'

describe('firstPlaceholder', () => {
  it('框住整个【占位符】，含书名号本身', () => {
    // 选区要连书名号一起选中：用户接着打字是要把「【XX话题】」整个替换掉，
    // 只选中间的字会留下一对空书名号。
    const p = '帮我调研【XX话题】，输出结构化报告'
    const r = firstPlaceholder(p)!
    expect(p.slice(r.start, r.end)).toBe('【XX话题】')
  })

  it('只认第一个', () => {
    const p = '把【A】译成【B】'
    const r = firstPlaceholder(p)!
    expect(p.slice(r.start, r.end)).toBe('【A】')
  })

  it('没有占位符时返回 null', () => {
    expect(firstPlaceholder('帮我做人生设计，规划未来五年的三个不同人生版本。')).toBeNull()
  })

  it('只有半边书名号不算', () => {
    // 真出现这种数据宁可当作"没有占位符"，也不要算出一个越界的选区。
    expect(firstPlaceholder('帮我处理【未闭合')).toBeNull()
  })
})

describe('applyScenario', () => {
  const P = '帮我调研【XX话题】，输出结构化报告'

  it('输入框空着就整段替换，并选中占位符', () => {
    const r = applyScenario('', P)
    expect(r.value).toBe(P)
    expect(r.value.slice(r.start, r.end)).toBe('【XX话题】')
  })

  it('只有空白也算空', () => {
    expect(applyScenario('   \n ', P).value).toBe(P)
  })

  it('已经打了字就另起一段追加，不覆盖', () => {
    // 场景提示词是一整段模板，直接顶掉别人写了一半的话，损失比省下一次点击大得多。
    const r = applyScenario('先看看这个文件', P)
    expect(r.value).toBe('先看看这个文件\n\n' + P)
    // 选区要落在**新插进去**的那一段上，不是原文里的位置。
    expect(r.value.slice(r.start, r.end)).toBe('【XX话题】')
  })

  it('没有占位符时把光标放到末尾', () => {
    const bare = '帮我做人生设计'
    const r = applyScenario('', bare)
    expect(r.start).toBe(bare.length)
    expect(r.end).toBe(bare.length)
  })
})

describe('skillHint', () => {
  it('点名技能，末尾换行把任务另起一段', () => {
    expect(skillHint('cn-docx')).toBe('请使用「cn-docx」技能完成以下任务：\n')
  })

  it('没有关联技能就什么都不加', () => {
    // OA 那三条走 MCP、录音纪要是内置功能，都没有 skill。凭空插一句「请使用「」技能」
    // 是纯噪音，还会把占位符的位置一起推偏。
    expect(skillHint('')).toBe('')
  })

  it('拼在提示词前面时，占位符选区跟着后移', () => {
    // 这条是 skillHint 与 applyScenario 的接缝：前缀长度必须算进选区，否则填完
    // 选中的是提示词中间一段莫名其妙的字。
    const prompt = '帮我调研【XX话题】，输出结构化报告'
    const r = applyScenario('', skillHint('research') + prompt)
    expect(r.value.slice(r.start, r.end)).toBe('【XX话题】')
    expect(r.value.startsWith('请使用「research」技能')).toBe(true)
  })

  it('输入框已有内容 + 技能前缀，两层偏移都要算上', () => {
    const r = applyScenario('先看看这个', skillHint('research') + '帮我调研【XX话题】')
    expect(r.value.slice(r.start, r.end)).toBe('【XX话题】')
  })
})

describe('内置场景数据', () => {
  const all = SCENARIOS.flatMap((c) => c.items)

  it('45 个默认场景，11 个分类', () => {
    // 对齐产品侧那张表里「默认场景 = 是/新增」的 44 + 1 行。数字变了说明重新导过，
    // 顺手确认一下是不是有意的。
    expect(all).toHaveLength(45)
    expect(SCENARIOS).toHaveLength(11)
  })

  it('分类 id 与场景 id 都不重复', () => {
    // id 是 React key，也是将来和网桥清单对齐的主键；重了会静默渲染错行。
    expect(new Set(SCENARIOS.map((c) => c.id)).size).toBe(SCENARIOS.length)
    expect(new Set(all.map((s) => s.id)).size).toBe(all.length)
  })

  it('每条都有名字、提示词和描述', () => {
    // 描述空着的话二级面板里那一行会塌一半——表里 OA 后两条本来就是空的（合并单元格），
    // 导出时按分类继承补上了，这条测试盯的就是那个继承没丢。
    const bad = all.filter((s) => !s.name.trim() || !s.prompt.trim() || !s.blurb.trim())
    expect(bad.map((s) => s.id)).toEqual([])
  })

  it('id 与关联 skill 都是合法资源名', () => {
    // 后端 validResourceName 只收字母数字和 - _。表里 Scientific Agent Skills /
    // Claude Scholar 这种带空格的名字装不进本地技能目录，导出时已按此过滤成空串。
    const ok = /^[A-Za-z0-9_-]{1,64}$/
    expect(all.filter((s) => !ok.test(s.id)).map((s) => s.id)).toEqual([])
    expect(all.filter((s) => s.skill && !ok.test(s.skill)).map((s) => s.skill)).toEqual([])
  })

  it('内置功能分类确实存在，且只有一条', () => {
    // 录音纪要点了直接开录、不展开二级面板，所以它下面多出第二条就会点不到。
    for (const key of Object.keys(BUILTIN_CATEGORY)) {
      const cat = SCENARIOS.find((c) => c.id === key)
      expect(cat, `内置分类 ${key} 不在场景表里`).toBeTruthy()
      expect(cat!.items).toHaveLength(1)
    }
  })
})
