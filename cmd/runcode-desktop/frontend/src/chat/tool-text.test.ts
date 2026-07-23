import { describe, it, expect } from 'vitest'
import type { ToolEvent } from '@/core/bridge'
import {
  analyzeSteps,
  askPayload,
  diffStats,
  failText,
  formatInput,
  hasDiff,
  lineClass,
  parseToolInput,
  taskActivity,
  taskMeta,
  toolLabel,
  toolTargetPath,
  toolVerbTarget,
} from './tool-text'

const ev = (t: Partial<ToolEvent>): ToolEvent => ({ type: 'completed', ...t }) as ToolEvent

describe('parseToolInput', () => {
  it('passes a live object through', () => {
    expect(parseToolInput({ path: 'a.ts' })).toEqual({ path: 'a.ts' })
  })
  it('parses the resumed JSON-string form', () => {
    expect(parseToolInput('{"path":"a.ts"}')).toEqual({ path: 'a.ts' })
  })
  it('falls back to an empty object on unparseable / non-object input', () => {
    expect(parseToolInput('not json')).toEqual({})
    expect(parseToolInput('123')).toEqual({}) // valid JSON, but not an object
    expect(parseToolInput(null)).toEqual({})
    expect(parseToolInput('')).toEqual({})
    expect(parseToolInput(42)).toEqual({})
  })
})

describe('toolVerbTarget', () => {
  it('collapses and clips a Bash command', () => {
    const { verb, target } = toolVerbTarget(ev({ toolName: 'Bash', input: { command: '  go   test\n./...  ' } }))
    expect(verb).toBe('运行命令')
    expect(target).toBe('go test ./...')
  })
  it('clips an over-long command with an ellipsis', () => {
    const { target } = toolVerbTarget(ev({ toolName: 'Bash', input: { command: 'x'.repeat(80) } }))
    expect(target).toHaveLength(57) // 56 chars + '…'
    expect(target.endsWith('…')).toBe(true)
  })
  it('shows the host for WebFetch and the raw url when unparseable', () => {
    expect(toolVerbTarget(ev({ toolName: 'WebFetch', input: { url: 'https://example.com/a/b' } })).target).toBe('example.com')
    expect(toolVerbTarget(ev({ toolName: 'WebFetch', input: { url: 'not a url' } })).target).toBe('not a url')
  })
  it('uses the pattern for Grep/Glob and the query for WebSearch', () => {
    expect(toolVerbTarget(ev({ toolName: 'Grep', input: { pattern: 'foo.*bar' } })).target).toBe('foo.*bar')
    expect(toolVerbTarget(ev({ toolName: 'WebSearch', input: { query: 'runcode' } })).target).toBe('runcode')
  })
  it('falls back to the file basename, preferring the event files over the input path', () => {
    expect(toolVerbTarget(ev({ toolName: 'Read', files: [{ path: 'a/b/c.ts' }], input: { path: 'x/y.ts' } })).target).toBe('c.ts')
    expect(toolVerbTarget(ev({ toolName: 'Read', input: { path: 'x/y.ts' } })).target).toBe('y.ts')
  })
  it('keeps an unmapped tool name as its own verb', () => {
    expect(toolVerbTarget(ev({ toolName: 'mcp__x__y' })).verb).toBe('mcp__x__y')
    expect(toolVerbTarget(ev({})).verb).toBe('工具')
  })
})

describe('toolTargetPath', () => {
  it('prefers the event file, then the input path, else undefined', () => {
    expect(toolTargetPath(ev({ toolName: 'Write', files: [{ path: 'a.ts' }] }))).toBe('a.ts')
    expect(toolTargetPath(ev({ toolName: 'Write', input: { path: 'b.ts' } }))).toBe('b.ts')
    expect(toolTargetPath(ev({ toolName: 'Write' }))).toBeUndefined()
    expect(toolTargetPath(ev({ toolName: 'Write', input: { path: 123 } }))).toBeUndefined()
  })
})

describe('diffStats / toolLabel', () => {
  const edited = ev({
    toolName: 'Edit',
    files: [{ path: 'src/a.ts' }],
    output: [
      { stream: 'diff_add', text: '+a' },
      { stream: 'diff_add', text: '+b' },
      { stream: 'diff_del', text: '-c' },
      { stream: 'diff_context', text: ' d' },
    ],
  })
  it('counts added and deleted diff lines only', () => {
    expect(diffStats(edited)).toEqual({ add: 2, del: 1 })
    expect(hasDiff(edited)).toBe(true)
    expect(hasDiff(ev({ toolName: 'Read' }))).toBe(false)
  })
  it('appends the churn to the flat label only when there is churn', () => {
    expect(toolLabel(edited)).toBe('编辑 a.ts  +2 -1')
    expect(toolLabel(ev({ toolName: 'Read', input: { path: 'a.ts' } }))).toBe('读取文件 a.ts')
  })
})

describe('failText', () => {
  it('translates a known deny reason', () => {
    expect(failText(ev({ message: 'denied:read_required' }))).toBe('需先读取该文件再写入')
  })
  it('falls back to a generic denial for an unknown reason', () => {
    expect(failText(ev({ message: 'denied:whatever' }))).toBe('权限被拒绝')
  })
  it('translates known failures and passes anything else through', () => {
    expect(failText(ev({ message: 'cancelled' }))).toBe('已取消')
    expect(failText(ev({ message: 'boom' }))).toBe('boom')
    expect(failText(ev({}))).toBe('失败')
  })
})

describe('lineClass', () => {
  it('colors stderr red, info faint and anything else muted', () => {
    expect(lineClass('stderr')).toContain('text-red')
    expect(lineClass('info')).toContain('text-faint')
    expect(lineClass('stdout')).toContain('text-muted')
    expect(lineClass(undefined)).toContain('text-muted')
  })
})

describe('analyzeSteps / askPayload', () => {
  it('reads the analyze protocol from both input forms', () => {
    const live = analyzeSteps({ method: 'm', steps: [{ key: 'a', label: 'A', content: 'x' }] })
    expect(live).toEqual({ method: 'm', steps: [{ key: 'a', label: 'A', content: 'x' }] })
    const resumed = analyzeSteps('{"method":"m","steps":[{"key":"a","content":"x"}]}')
    expect(resumed.steps[0]).toEqual({ key: 'a', label: undefined, content: 'x' })
  })
  it('tolerates a missing / malformed steps list', () => {
    expect(analyzeSteps({ method: 'm' })).toEqual({ method: 'm', steps: [] })
    expect(analyzeSteps('garbage')).toEqual({ method: undefined, steps: [] })
  })
  it('reads the ask question and options, defaulting to empty', () => {
    expect(askPayload('{"question":"q","options":["a","b"]}')).toEqual({ question: 'q', options: ['a', 'b'] })
    expect(askPayload({})).toEqual({ question: '', options: [] })
    expect(askPayload({ question: 'q', options: 'nope' })).toEqual({ question: 'q', options: [] })
  })
})

describe('taskMeta / taskActivity', () => {
  it('prefers the declared sub-agent type, then the streamed name, then a default', () => {
    expect(taskMeta(ev({ input: { subagent_type: 'planner', description: 'd' } })).sub).toBe('planner')
    expect(taskMeta(ev({}), { agent: 'explorer', text: '', tools: [] }).sub).toBe('explorer')
    expect(taskMeta(ev({}))).toEqual({ sub: '子代理', desc: '' })
  })
  it('reports the running child tool, else the last streamed line', () => {
    const runningChild = ev({ type: 'progress', toolName: 'Bash', input: { command: 'ls' } })
    expect(taskActivity({ agent: 'a', text: '', tools: [runningChild] })).toBe('运行命令 ls')
    expect(taskActivity({ agent: 'a', text: 'first\nlast\n  \n', tools: [] })).toBe('last')
    expect(taskActivity({ agent: 'a', text: '', tools: [] })).toBe('')
    expect(taskActivity(undefined)).toBe('')
  })
})

describe('formatInput', () => {
  it('pretty-prints objects and JSON strings', () => {
    expect(formatInput({ a: 1 })).toBe('{\n  "a": 1\n}')
    expect(formatInput('{"a":1}')).toBe('{\n  "a": 1\n}')
  })
  it('keeps an unparseable string verbatim and blanks empty input', () => {
    expect(formatInput('raw text')).toBe('raw text')
    expect(formatInput('')).toBe('')
    expect(formatInput(null)).toBe('')
  })
})
