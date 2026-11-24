// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { ToolEvent } from '@/core/bridge'
import { automationRunToTool } from './automation-run'
import { ExecutionCard } from './execution-card'

let root: Root
let host: HTMLDivElement

beforeEach(() => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

function runTool(status: string, prompt = '整理 <b>今天</b>'): ToolEvent {
  return automationRunToTool({
    id: 'run-1', taskId: 'task', taskRevision: 1, runRevision: 1, status,
    scheduledAtUTC: '2026-11-18T01:00:00Z', name: '每日整理记录', prompt,
    workspace: 'D:\\notes', executionMode: 'reuse_session', summary: '好了',
    schedule: { mode: 'daily', timeZone: 'Asia/Shanghai', startDate: '2026-11-18', timeOfDay: '09:00:00' },
  })
}

async function render(tools: ToolEvent[]) {
  await act(async () => { root.render(<ExecutionCard tools={tools} />) })
}

describe('ExecutionCard automation summary', () => {
  it('stays collapsed until clicked, and only a success is a green check', async () => {
    await render([runTool('running'), { type: 'progress', toolName: 'Bash', toolUseID: 'bash', input: { command: 'go test' } }])
    expect(host.textContent).toContain('执行')
    expect(host.textContent).toContain('go test')
    expect(host.textContent).not.toContain('本次任务内容')
    expect(host.querySelector('.text-green')).toBeNull()
    expect(host.querySelector('.spin-ring')).not.toBeNull()

    const row = [...host.querySelectorAll('.cursor-pointer')].find((el) => el.textContent?.includes('每日整理记录'))
    await act(async () => { (row as HTMLElement | undefined)?.click() })
    expect(host.textContent).toContain('本次任务内容')
    expect(host.textContent).toContain('整理 <b>今天</b>')
    expect(host.querySelector('b')).toBeNull()

    await render([runTool('succeeded')])
    expect(host.querySelector('.text-green')).not.toBeNull()
    for (const status of ['failed', 'interrupted', 'timed_out', 'skipped']) {
      await render([runTool(status)])
      expect(host.querySelector('.text-green'), status).toBeNull()
    }
  })
})
