import { describe, it, expect } from 'vitest'
import { applyMention, computeMention, matchByNameOrDesc, rankFileMatches } from './mention'

// computeMention 是 composer 三种触发（@技能 / /子代理 / #文件）的解析核心：
// '@' 与 '/' 只在输入最开头生效，'#' 可出现在任意空白之后；触发符到光标之间
// 不得有空白。此前它是零测试的纯函数（审核遗留项）。
describe('computeMention', () => {
  it('recognizes @ at the start of input', () => {
    expect(computeMention('@ski', 4)).toEqual({ query: 'ski', start: 0, trigger: '@' })
    expect(computeMention('@', 1)).toEqual({ query: '', start: 0, trigger: '@' })
  })
  it('recognizes / at the start of input', () => {
    expect(computeMention('/agent', 6)).toEqual({ query: 'agent', start: 0, trigger: '/' })
  })
  it('deactivates @ and / once the command contains whitespace', () => {
    expect(computeMention('@sk i', 5)).toBeNull()
    expect(computeMention('/a b', 4)).toBeNull()
  })
  it('does not treat mid-input @ or / as a trigger', () => {
    expect(computeMention('hi @x', 5)).toBeNull()
    expect(computeMention('a/b', 3)).toBeNull()
  })
  it('recognizes # at the start and after whitespace', () => {
    expect(computeMention('#a', 2)).toEqual({ query: 'a', start: 0, trigger: '#' })
    expect(computeMention('fix #main', 9)).toEqual({ query: 'main', start: 4, trigger: '#' })
  })
  it('rejects # glued to a word (no preceding whitespace)', () => {
    expect(computeMention('foo#bar', 7)).toBeNull()
  })
  it('deactivates # once the query contains whitespace', () => {
    expect(computeMention('see #a b', 8)).toBeNull()
  })
  it('uses only text up to the cursor', () => {
    expect(computeMention('#abcd', 3)).toEqual({ query: 'ab', start: 0, trigger: '#' })
    expect(computeMention('#abc', 0)).toBeNull() // 光标在触发符之前 = 无活动触发
  })
  it('lets a later # trigger win when the leading @ has gone stale', () => {
    expect(computeMention('@x #y', 5)).toEqual({ query: 'y', start: 3, trigger: '#' })
  })
})

describe('rankFileMatches', () => {
  const files = ['src/app/main.ts', 'main.ts', 'docs/readme.md', 'src/mainframe/x.ts']
  it('orders shortest path first when the query is empty', () => {
    // 空查询没有 basename 前缀可比，排序退化为「路径短的在前」——浅层文件更可能是想引用的那个。
    expect(rankFileMatches(files, '')).toEqual(['main.ts', 'docs/readme.md', 'src/app/main.ts', 'src/mainframe/x.ts'])
  })
  it('keeps only substring hits anywhere in the path', () => {
    expect(rankFileMatches(files, 'readme')).toEqual(['docs/readme.md'])
    expect(rankFileMatches(files, 'zzz')).toEqual([])
  })
  it('ranks basename-prefix hits first, then the shorter path', () => {
    // 'main.ts' 与 'src/app/main.ts' 的 basename 都以 main 开头 → 短的在前；
    // 'src/mainframe/x.ts' 只是路径里含 main，basename 不匹配 → 排最后。
    expect(rankFileMatches(files, 'main')).toEqual(['main.ts', 'src/app/main.ts', 'src/mainframe/x.ts'])
  })
  it('caps the list at the limit', () => {
    expect(rankFileMatches(['a', 'b', 'c'], '', 2)).toHaveLength(2)
  })
})

describe('matchByNameOrDesc', () => {
  const items = [
    { name: 'ppt-maker', description: '制作演示文稿' },
    { name: 'doc', description: '写 PPT 大纲' },
    { name: 'other', description: 'x' },
  ]
  it('matches on either the name or the description, case-insensitively', () => {
    expect(matchByNameOrDesc(items, 'ppt').map((i) => i.name)).toEqual(['ppt-maker', 'doc'])
  })
  it('returns everything for an empty query', () => {
    expect(matchByNameOrDesc(items, '')).toHaveLength(3)
  })
  it('中文展示名/展示描述也能搜到——市场装来的技能，用户只认得那两句', () => {
    const withDisplay = [{
      name: 'cn-docx', displayName: '中文公文',
      displayDescription: '规范化参考文献',
      description: 'Use when normalizing academic references',
    }]
    const hit = (q: string) => matchByNameOrDesc(withDisplay, q).map((i) => i.name)
    expect(hit('公文')).toEqual(['cn-docx'])     // 展示名
    expect(hit('规范化')).toEqual(['cn-docx'])   // 展示描述
    expect(hit('docx')).toEqual(['cn-docx'])     // 真实 name
    expect(hit('normalizing')).toEqual(['cn-docx']) // 给模型看的那句
    expect(hit('路由器')).toEqual([])
  })
})

describe('applyMention', () => {
  it('splices a # file reference inline and keeps the rest of the line', () => {
    // 补一个空格后把光标停在引用之后，用户接着写；' rest' 是触发之后的原文，原样保留。
    expect(applyMention('fix #ma rest', { start: 4, query: 'ma', trigger: '#' }, 'src/main.ts'))
      .toEqual({ value: 'fix #src/main.ts  rest', caret: 'fix #src/main.ts '.length })
  })
  it('replaces a @ command with the skill instruction', () => {
    expect(applyMention('@pp', { start: 0, query: 'pp', trigger: '@' }, 'ppt-maker'))
      .toEqual({ value: '请使用「ppt-maker」技能完成：', caret: '请使用「ppt-maker」技能完成：'.length })
  })
  it('replaces a / command with the delegation instruction, keeping trailing text', () => {
    const r = applyMention('/rev 分析这段代码', { start: 0, query: 'rev', trigger: '/' }, 'code-reviewer')
    expect(r.value).toBe('请委派「code-reviewer」子代理完成： 分析这段代码')
    expect(r.caret).toBe('请委派「code-reviewer」子代理完成：'.length)
  })
})
