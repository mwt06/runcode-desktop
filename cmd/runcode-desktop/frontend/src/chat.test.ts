import { describe, it, expect } from 'vitest'
import { mergeTool, groupBlocks, parsePlan, finalizeTools, type Block } from './chat'
import type { EditRecord, ToolEvent } from './bridge'

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
