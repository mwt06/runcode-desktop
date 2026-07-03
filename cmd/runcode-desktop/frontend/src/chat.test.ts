import { describe, it, expect } from 'vitest'
import { mergeTool, groupBlocks, parsePlan, finalizeTools, type Block } from './chat'
import type { ToolEvent } from './bridge'

const ev = (o: Partial<ToolEvent>): ToolEvent => ({ type: 'started', ...o })
const line = (text: string) => ({ stream: 'stdout', text })

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
