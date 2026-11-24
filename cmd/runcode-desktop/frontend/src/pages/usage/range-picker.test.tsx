// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { UsageRangeSelection } from '@/core/usage'
import { RangePicker } from './range-picker'

const NOW = new Date('2026-11-10T01:30:45+08:00')
let host: HTMLDivElement
let root: Root

function setInput(el: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(el, value)
  el.dispatchEvent(new Event('input', { bubbles: true }))
}
const button = (label: string) => [...host.querySelectorAll('button')].find((b) => b.textContent?.trim() === label) as HTMLButtonElement
const input = (label: string) => host.querySelector(`input[aria-label="${label}"]`) as HTMLInputElement

async function render(value: UsageRangeSelection, onApply: (s: UsageRangeSelection) => void) {
  await act(async () => root.render(<RangePicker value={value} onApply={onApply} now={() => NOW} />))
}

beforeEach(() => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

describe('RangePicker', () => {
  it('shanghaiRangesAndDraftCancel', async () => {
    const onApply = vi.fn()
    await render({ kind: 'today' }, onApply)
    expect(button('当天').getAttribute('aria-pressed')).toBe('true')

    await act(async () => button('近7天').click())
    expect(onApply).toHaveBeenLastCalledWith({ kind: 'last7d' })

    // 打开自定义：默认草稿是上海当天 00:00 到现在；取消不提交并把焦点还给触发按钮。
    const trigger = button('自定义…')
    await act(async () => trigger.click())
    expect(input('开始时间').value).toBe('2026-11-10T00:00')
    expect(input('结束时间').value).toBe('2026-11-10T01:30')
    await act(async () => setInput(input('开始时间'), '2026-11-09T08:00'))
    await act(async () => button('取消').click())
    expect(onApply).toHaveBeenCalledTimes(1)
    expect(host.querySelector('input[aria-label="开始时间"]')).toBeNull()
    expect(document.activeElement).toBe(trigger)

    // Escape 同样是取消。
    await act(async () => trigger.click())
    await act(async () => { input('开始时间').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
    expect(host.querySelector('input[aria-label="开始时间"]')).toBeNull()
    expect(onApply).toHaveBeenCalledTimes(1)

    // 非法范围：给出原因、确定不可用、原筛选不变。
    await act(async () => trigger.click())
    await act(async () => setInput(input('开始时间'), '2026-11-10T01:00'))
    await act(async () => setInput(input('结束时间'), '2026-11-10T00:30'))
    expect(host.textContent).toContain('开始时间必须早于结束时间')
    expect(button('确定').disabled).toBe(true)
    await act(async () => button('确定').click())
    expect(onApply).toHaveBeenCalledTimes(1)

    // 合法范围：分钟级、带 +08:00 偏移提交。
    await act(async () => setInput(input('开始时间'), '2026-11-09T08:00'))
    await act(async () => button('确定').click())
    expect(onApply).toHaveBeenLastCalledWith({ kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', endAt: '2026-11-10T00:30:00+08:00', followNow: false })
    expect(document.activeElement).toBe(trigger)
  })

  it('跟随当前时刻时结束时间不可编辑，提交不带结束时间', async () => {
    const onApply = vi.fn()
    await render({ kind: 'today' }, onApply)
    await act(async () => button('自定义…').click())
    const follow = host.querySelector('input[type="checkbox"]') as HTMLInputElement
    await act(async () => follow.click())
    expect(input('结束时间').disabled).toBe(true)
    await act(async () => setInput(input('开始时间'), '2026-11-09T08:00'))
    await act(async () => button('确定').click())
    expect(onApply).toHaveBeenLastCalledWith({ kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', followNow: true })
  })

  it('已应用的自定义范围显示为按钮文字，并作为下次打开的草稿', async () => {
    await render({ kind: 'custom', startAt: '2026-11-09T08:00:00+08:00', endAt: '2026-11-10T00:30:00+08:00', followNow: false }, vi.fn())
    const trigger = [...host.querySelectorAll('button')].find((b) => b.getAttribute('aria-pressed') === 'true')!
    expect(trigger.textContent).toContain('2026-11-09 08:00 — 2026-11-10 00:30')
    await act(async () => trigger.click())
    expect(input('开始时间').value).toBe('2026-11-09T08:00')
    expect(input('结束时间').value).toBe('2026-11-10T00:30')
  })
})
