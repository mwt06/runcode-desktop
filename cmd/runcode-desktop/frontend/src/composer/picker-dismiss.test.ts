import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPickerDismiss, PICKER_DISMISS_DELAY } from './picker-dismiss'

describe('picker dismissal', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('waits briefly before closing', () => {
    const close = vi.fn()
    createPickerDismiss(close).schedule()
    vi.advanceTimersByTime(PICKER_DISMISS_DELAY - 1)
    expect(close).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(close).toHaveBeenCalledOnce()
  })
  it('cancels when the pointer returns across the gap', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    vi.advanceTimersByTime(100)
    dismiss.cancel()
    vi.advanceTimersByTime(500)
    expect(close).not.toHaveBeenCalled()
  })
  it('does not keep postponing on repeated leave events', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    vi.advanceTimersByTime(150)
    dismiss.schedule()
    vi.advanceTimersByTime(50)
    expect(close).toHaveBeenCalledOnce()
  })
  it('immediate dismissal clears the old timer', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    dismiss.close()
    vi.advanceTimersByTime(500)
    expect(close).toHaveBeenCalledOnce()
    expect(vi.getTimerCount()).toBe(0)
  })
  it('cleanup on category change or unmount cannot later close a new menu', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    vi.advanceTimersByTime(150)
    dismiss.cancel()
    dismiss.schedule()
    vi.advanceTimersByTime(50)
    expect(close).not.toHaveBeenCalled()
    vi.advanceTimersByTime(150)
    expect(close).toHaveBeenCalledOnce()
  })
  it('can schedule another close after one has fired', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    vi.advanceTimersByTime(200)
    dismiss.schedule()
    vi.advanceTimersByTime(200)
    expect(close).toHaveBeenCalledTimes(2)
  })
})

describe('keyboard continuation after pointer leave', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('keeps keyboard-only pickers open until an actual leave is scheduled', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.cancel()
    vi.advanceTimersByTime(5000)
    expect(close).not.toHaveBeenCalled()
  })
  it('typing or navigating cancels a pending close, without preventing a later leave', () => {
    const close = vi.fn(), dismiss = createPickerDismiss(close)
    dismiss.schedule()
    vi.advanceTimersByTime(199)
    dismiss.cancel()
    vi.advanceTimersByTime(1000)
    expect(close).not.toHaveBeenCalled()
    dismiss.schedule()
    vi.advanceTimersByTime(200)
    expect(close).toHaveBeenCalledOnce()
  })
})
