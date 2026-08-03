import { describe, it, expect } from 'vitest'
import { mergeTool, groupBlocks, parsePlan, resumedPlan, finalizeTools, resumedMatchedFiles, turnErrorText, turnProducedText, retryReasonLabel, type Block } from './blocks'
import type { EditRecord, ResumedBlock, ToolEvent } from '@/core/bridge'

describe('turnProducedText', () => {
  const user = (text: string): Block => ({ kind: 'user', id: text, text, ts: '' })
  const asst = (text: string): Block => ({ kind: 'assistant', id: text, text, streaming: false, ts: '' })

  it('只看最后一个 user 之后的块——上一轮的回复不算这一轮', () => {
    // 平台模型答过一次,切本地模型后再问却空返回:本轮没有助手文本。
    const blocks = [user('你好'), asst('我是 DeepSeek…'), user('你好')]
    expect(turnProducedText(blocks)).toBe(false)
  })

  it('本轮确有助手文本时为真', () => {
    expect(turnProducedText([user('你好'), asst('我是 DeepSeek…'), user('你好'), asst('你好呀')])).toBe(true)
  })

  it('本轮助手块只有空白不算产出', () => {
    expect(turnProducedText([user('hi'), asst('   ')])).toBe(false)
  })
})

describe('turnErrorText', () => {
  it('只在用户点了停止时吞掉取消类错误', () => {
    expect(turnErrorText('context canceled', true)).toBeNull()
    expect(turnErrorText('request cancelled', true)).toBeNull()
  })

  it('非用户主动的取消必须显示(否则错误了却看不到原因)', () => {
    expect(turnErrorText('context canceled', false)).toBe('context canceled')
    expect(turnErrorText('upstream canceled the request', false)).toBe('upstream canceled the request')
  })

  it('真正的报错无论是否停止都原样显示', () => {
    expect(turnErrorText('openai: 429 rate limited', true)).toBe('openai: 429 rate limited')
    expect(turnErrorText('dial tcp: connection refused', false)).toBe('dial tcp: connection refused')
  })

  it('空原因兜底成可读文案,不渲染空红块', () => {
    expect(turnErrorText('', false)).toBe('回合失败（未返回具体原因）')
    expect(turnErrorText('   ', true)).toBe('回合失败（未返回具体原因）')
  })
})

describe('retryReasonLabel', () => {
  it('把裸错误类翻成中文', () => {
    expect(retryReasonLabel('transport')).toBe('连接中断')
    expect(retryReasonLabel('server')).toBe('服务端错误')
    expect(retryReasonLabel('rate_limited')).toBe('请求过于频繁')
  })

  it('保留 (HTTP nnn) 后缀', () => {
    expect(retryReasonLabel('server (HTTP 503)')).toBe('服务端错误 (HTTP 503)')
    expect(retryReasonLabel('rate_limited (HTTP 429)')).toBe('请求过于频繁 (HTTP 429)')
  })

  it('未知或已本地化的原因原样透传', () => {
    expect(retryReasonLabel('连接中断')).toBe('连接中断')
    expect(retryReasonLabel('something else')).toBe('something else')
  })
})

const ev = (o: Partial<ToolEvent>): ToolEvent => ({ type: 'started', ...o })
const line = (text: string) => ({ stream: 'stdout', text })

function editTool(id: string, tuid: string, rel: string, snapshotId: string, added: number): Block {
  const rec: EditRecord = { snapshotId, toolUseId: tuid, relPath: rel, added, removed: 0, created: false }
  return { kind: 'tool', id, tool: { type: 'completed', toolName: 'Write', toolUseID: tuid, data: rec } }
}

describe('mergeTool', () => {
  it('appends streamed output across progress events', () => {
    let t = mergeTool(undefined, ev({ type: 'started', toolUseID: 't', toolName: 'Bash' }))
    t = mergeTool(t, ev({ type: 'progress', output: [line('a'), line('b')] }))
    t = mergeTool(t, ev({ type: 'progress', output: [line('c')] }))
    expect(t.output?.map((l) => l.text)).toEqual(['a', 'b', 'c'])
  })

  it('replaces the live tail with the completed event so lines are not duplicated', () => {
    let t = mergeTool(undefined, ev({ type: 'progress', toolUseID: 't', output: [line('live1'), line('live2')] }))
    t = mergeTool(t, ev({ type: 'completed', output: [line('final')] }))
    expect(t.output?.map((l) => l.text)).toEqual(['final'])
    expect(t.type).toBe('completed')
  })

  it('caps the live tail to the last 400 lines', () => {
    let t: ToolEvent | undefined
    for (let i = 0; i < 500; i++) t = mergeTool(t, ev({ type: 'progress', output: [line(String(i))] }))
    expect(t!.output!.length).toBe(400)
    expect(t!.output![0].text).toBe('100')
    expect(t!.output![399].text).toBe('499')
  })

  it('keeps earlier files when a later event carries none', () => {
    let t = mergeTool(undefined, ev({ type: 'progress', files: [{ path: 'a.go' }] }))
    t = mergeTool(t, ev({ type: 'completed' }))
    expect(t.files?.[0].path).toBe('a.go')
  })
})

describe('groupBlocks', () => {
  const toolB = (id: string, name: string): Block => ({ kind: 'tool', id, tool: ev({ toolName: name, toolUseID: id }) })

  it('drops TodoWrite, coalesces plain tools, and splits special tools', () => {
    const blocks: Block[] = [
      toolB('1', 'Read'),
      toolB('2', 'Bash'),
      toolB('3', 'TodoWrite'),
      toolB('4', 'AskUser'),
      toolB('5', 'Analyze'),
      toolB('6', 'Task'),
      { kind: 'assistant', id: '7', text: 'hi', streaming: false, ts: '' },
    ]
    const groups = groupBlocks(blocks)
    expect(groups.map((g) => g.kind)).toEqual(['exec', 'ask', 'analyze', 'block', 'block'])
    const exec = groups[0]
    expect(exec.kind === 'exec' && exec.tools.length).toBe(2)
  })

  it('技能加载单独成卡,不被折进相邻的工具组', () => {
    // 一次「加载技能」是对话的转折点(模型自此按那套流程走),压进 grep/read 的
    // 折叠列表里就看不见了。
    const groups = groupBlocks([toolB('1', 'Read'), toolB('2', 'Skill'), toolB('3', 'Grep')])
    expect(groups.map((g) => g.kind)).toEqual(['exec', 'skill', 'exec'])
  })
})

describe('groupBlocks taskgroup', () => {
  const task = (id: string): Block => ({ kind: 'tool', id, tool: ev({ toolName: 'Task', toolUseID: id }) })

  it('merges adjacent Task calls into one taskgroup, keeping order', () => {
    const g = groupBlocks([task('1'), task('2'), task('3')])
    expect(g).toHaveLength(1)
    expect(g[0].kind === 'taskgroup' && g[0].tasks.map((t) => t.id)).toEqual(['1', '2', '3'])
  })

  it('keeps a lone Task as a plain block', () => {
    const g = groupBlocks([task('1')])
    expect(g.map((x) => x.kind)).toEqual(['block'])
  })

  it('does not group Task calls separated by a visible tool', () => {
    const g = groupBlocks([task('1'), { kind: 'tool', id: '2', tool: ev({ toolName: 'Read' }) }, task('3')])
    expect(g.map((x) => x.kind)).toEqual(['block', 'exec', 'block'])
  })

  it('groups across an invisible TodoWrite between Task calls', () => {
    const g = groupBlocks([task('1'), { kind: 'tool', id: '2', tool: ev({ toolName: 'TodoWrite' }) }, task('3')])
    expect(g).toHaveLength(1)
    expect(g[0].kind).toBe('taskgroup')
  })
})

describe('parsePlan', () => {
  it('reads the snapshot from event data and recomputes missing counts', () => {
    const e = ev({ toolName: 'TodoWrite' }) as ToolEvent & { data?: unknown }
    e.data = {
      items: [
        { content: 'a', status: 'completed' },
        { content: 'b', status: 'in_progress', activeForm: 'Doing b' },
      ],
    }
    const plan = parsePlan(e)
    expect(plan).not.toBeNull()
    expect(plan!.total).toBe(2)
    expect(plan!.done).toBe(1)
    expect(plan!.items[1].activeForm).toBe('Doing b')
  })

  it('returns null when there is no structured data (started/completed events)', () => {
    expect(parsePlan(ev({ toolName: 'TodoWrite' }))).toBeNull()
  })
})

describe('resumedPlan', () => {
  const todoBlock = (input: string): ResumedBlock => ({
    kind: 'tool',
    tool: { toolName: 'TodoWrite', toolUseId: 't1', input, isError: false },
  })
  const other: ResumedBlock = { kind: 'tool', tool: { toolName: 'Read', toolUseId: 'r1', isError: false } }

  it('重建进度板:取最后一次 TodoWrite 的参数(每次调用都替换整张清单)', () => {
    const older = todoBlock(JSON.stringify({ todos: [{ content: '旧清单', status: 'pending' }] }))
    const latest = todoBlock(JSON.stringify({
      todos: [
        { content: '取素材', status: 'completed' },
        { content: '画图', status: 'in_progress', activeForm: '正在画图' },
        { content: '合稿', status: 'pending' },
      ],
    }))
    const plan = resumedPlan([older, other, latest])
    expect(plan).not.toBeNull()
    expect(plan!.total).toBe(3)
    expect(plan!.done).toBe(1)
    expect(plan!.items[1].activeForm).toBe('正在画图')
  })

  it('没记过待办的会话不出胶囊', () => {
    expect(resumedPlan([other])).toBeNull()
    expect(resumedPlan([])).toBeNull()
    expect(resumedPlan(null)).toBeNull()
  })

  it('参数损坏或清单为空时宁可不显示,也不显示一个错的板', () => {
    expect(resumedPlan([todoBlock('{"todos":')])).toBeNull()
    expect(resumedPlan([todoBlock(JSON.stringify({ todos: [] }))])).toBeNull()
  })
})

describe('finalizeTools', () => {
  it('marks a still-running tool and its nested child as cancelled', () => {
    const blocks: Block[] = [
      {
        kind: 'tool',
        id: '1',
        tool: ev({ type: 'progress', toolName: 'Bash' }),
        nested: { agent: 'x', text: '', tools: [ev({ type: 'started', toolName: 'Write' })] },
      },
    ]
    const [b] = finalizeTools(blocks)
    if (b.kind !== 'tool') throw new Error('expected tool block')
    expect(b.tool.type).toBe('failed')
    expect(b.tool.message).toBe('cancelled')
    expect(b.nested!.tools[0].type).toBe('failed')
  })

  it('leaves completed tools untouched', () => {
    const done = ev({ type: 'completed', toolName: 'Read' })
    const [b] = finalizeTools([{ kind: 'tool', id: '1', tool: done }])
    expect(b.kind === 'tool' && b.tool).toBe(done)
  })
})

describe('mergeTool preserves edit metadata', () => {
  it('carries data from the completed event onto the started block', () => {
    const started: ToolEvent = { type: 'started', toolName: 'Write', toolUseID: 'tu1', input: {} }
    const rec: EditRecord = { snapshotId: '1', toolUseId: 'tu1', relPath: 'a.md', added: 3, removed: 0, created: true }
    const completed: ToolEvent = { type: 'completed', toolName: 'Write', toolUseID: 'tu1', data: rec }
    expect(mergeTool(started, completed).data).toEqual(rec)
  })
})

describe('groupBlocks edits group', () => {
  it('emits one edits group per turn, one card per file, at the turn end', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      { kind: 'assistant', id: 'a1', text: 'done', streaming: false, ts: '' },
    ]
    const g = groupBlocks(blocks)
    const edits = g.filter((x) => x.kind === 'edits')
    expect(edits).toHaveLength(1)
    expect(edits[0].kind === 'edits' && edits[0].edits.map((e) => e.relPath)).toEqual(['a.md'])
    // The edits group comes after the assistant block.
    const idxEdits = g.findIndex((x) => x.kind === 'edits')
    const idxAsst = g.findIndex((x) => x.kind === 'block' && x.block.kind === 'assistant')
    expect(idxEdits).toBeGreaterThan(idxAsst)
    // The Write step is still present in an exec group.
    expect(g.some((x) => x.kind === 'exec')).toBe(true)
  })

  it('dedupes two edits to the same file in a turn, keeping the latest stat', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      editTool('t2', 'tu2', 'a.md', '1', 5), // same baseline id, later stat
    ]
    const g = groupBlocks(blocks)
    const edits = g.find((x) => x.kind === 'edits')
    expect(edits && edits.kind === 'edits' && edits.edits).toHaveLength(1)
    expect(edits && edits.kind === 'edits' && edits.edits[0].added).toBe(5)
  })

  it('separates edits across two turns', () => {
    const blocks: Block[] = [
      { kind: 'user', id: 'u1', text: 'go', ts: '' },
      editTool('t1', 'tu1', 'a.md', '1', 3),
      { kind: 'user', id: 'u2', text: 'again', ts: '' },
      editTool('t2', 'tu2', 'b.md', '2', 4),
    ]
    const g = groupBlocks(blocks)
    expect(g.filter((x) => x.kind === 'edits')).toHaveLength(2)
  })
})

describe('resumedMatchedFiles', () => {
  it('rebuilds a Glob result text into matched file references, dropping the truncation marker', () => {
    const files = resumedMatchedFiles('Glob', { pattern: '**/*' }, 'a.go\ndocs/b.md\n[output truncated]')
    expect(files).toEqual([
      { path: 'a.go', kind: 'matched' },
      { path: 'docs/b.md', kind: 'matched' },
    ])
  })

  it('rebuilds Grep files_with_matches with the JSON-string input resumed sessions carry', () => {
    const files = resumedMatchedFiles('Grep', '{"pattern":"x","output_mode":"files_with_matches"}', 'src/a.ts\nsrc/b.ts')
    expect(files?.map((f) => f.path)).toEqual(['src/a.ts', 'src/b.ts'])
  })

  it('leaves non-file-list results as text', () => {
    // Grep content mode (the default) streams matching lines, not paths.
    expect(resumedMatchedFiles('Grep', { pattern: 'x' }, 'a.go:1:match line')).toBeNull()
    // Prose (e.g. a no-match message) must not be mistaken for a one-file list.
    expect(resumedMatchedFiles('Glob', {}, 'no files matched the pattern')).toBeNull()
    expect(resumedMatchedFiles('Bash', {}, 'a.go')).toBeNull()
    expect(resumedMatchedFiles('Glob', {}, '')).toBeNull()
  })
})
