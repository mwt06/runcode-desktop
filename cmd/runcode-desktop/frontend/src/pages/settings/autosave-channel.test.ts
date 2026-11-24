import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAutoSaveChannel } from './autosave-channel'

type Draft = { text: string; enabled: boolean }

const INITIAL: Draft = { text: '', enabled: false }

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((ok, fail) => {
    resolve = ok
    reject = fail
  })
  return { promise, resolve, reject }
}

afterEach(() => {
  vi.useRealTimers()
})

describe('createAutoSaveChannel', () => {
  it('does not write the initial value', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    await channel.flush()

    expect(write).not.toHaveBeenCalled()
    expect(channel.getSnapshot().dirtyKeys).toEqual([])
  })

  it('merges debounced edits into one latest snapshot', async () => {
    vi.useFakeTimers()
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'a', enabled: false }, 'debounced')
    channel.edit({ text: 'ab', enabled: true }, 'debounced')
    await vi.advanceTimersByTimeAsync(599)
    expect(write).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)

    expect(write).toHaveBeenCalledTimes(1)
    expect(write).toHaveBeenCalledWith({ text: 'ab', enabled: true })
    expect(channel.getSnapshot()).toMatchObject({
      phase: 'saved',
      draft: { text: 'ab', enabled: true },
      confirmed: { text: 'ab', enabled: true },
      dirtyKeys: [],
    })
  })

  it('writes immediate edits without waiting for the debounce', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: '', enabled: true }, 'immediate')
    await channel.flush()

    expect(write).toHaveBeenCalledTimes(1)
    expect(write).toHaveBeenCalledWith({ text: '', enabled: true })
  })

  it('keeps one write in flight and then writes the newest pending draft', async () => {
    const first = deferred<Draft>()
    const write = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'a', enabled: false }, 'deferred')
    const flushing = channel.flush()
    channel.edit({ text: 'b', enabled: false }, 'deferred')
    first.resolve({ text: 'a', enabled: false })
    await flushing

    expect(write).toHaveBeenCalledTimes(2)
    expect(write.mock.calls[1][0]).toEqual({ text: 'b', enabled: false })
    expect(channel.getSnapshot().draft.text).toBe('b')
    expect(channel.getSnapshot().confirmed.text).toBe('b')
  })

  it('does not let an old response overwrite a newer draft', async () => {
    const first = deferred<Draft>()
    const write = vi.fn()
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'old', enabled: false }, 'deferred')
    const flushing = channel.flush()
    channel.edit({ text: 'new', enabled: true }, 'deferred')
    first.resolve({ text: 'old', enabled: false })
    await flushing

    expect(write).toHaveBeenCalledTimes(2)
    expect(write.mock.calls[1][0]).toEqual({ text: 'new', enabled: true })
    expect(channel.getSnapshot().draft).toEqual({ text: 'new', enabled: true })
  })

  it('treats an explicit empty string as a real change', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({
      initial: { text: 'proxy', enabled: false },
      write,
      delayMs: 600,
    })

    channel.edit({ text: '', enabled: false }, 'deferred')
    await channel.flush()

    expect(write).toHaveBeenCalledWith({ text: '', enabled: false })
  })

  it('does not write invalid input and keeps the draft', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({
      initial: INITIAL,
      write,
      validate: (value) => value.text === 'bad' ? 'invalid text' : null,
      delayMs: 600,
    })

    channel.edit({ text: 'bad', enabled: false }, 'immediate')
    await channel.flush()

    expect(write).not.toHaveBeenCalled()
    expect(channel.getSnapshot()).toMatchObject({
      phase: 'invalid',
      error: 'invalid text',
      draft: { text: 'bad', enabled: false },
    })
  })

  it('defers writes until ready and flushes dirty data when readiness returns', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.setReady(false)
    channel.edit({ text: 'later', enabled: false }, 'immediate')
    await channel.flush()
    expect(write).not.toHaveBeenCalled()

    channel.setReady(true)
    await channel.flush()
    expect(write).toHaveBeenCalledWith({ text: 'later', enabled: false })
  })

  it('rebases untouched fields while preserving dirty fields', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'mine', enabled: false }, 'deferred')
    channel.rebase({ text: 'server', enabled: true })

    expect(channel.getSnapshot()).toMatchObject({
      draft: { text: 'mine', enabled: true },
      confirmed: { text: 'server', enabled: true },
      dirtyKeys: ['text'],
      phase: 'dirty',
    })
    expect(write).not.toHaveBeenCalled()
  })

  it('clears dirty state when the user changes back to the confirmed value', async () => {
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'draft', enabled: false }, 'debounced')
    channel.edit(INITIAL, 'debounced')
    await channel.flush()

    expect(write).not.toHaveBeenCalled()
    expect(channel.getSnapshot()).toMatchObject({ phase: 'idle', dirtyKeys: [] })
  })

  it('discards a pending edit without writing it', async () => {
    vi.useFakeTimers()
    const write = vi.fn(async (value: Draft) => value)
    const channel = createAutoSaveChannel({ initial: INITIAL, write, delayMs: 600 })

    channel.edit({ text: 'discard', enabled: false }, 'debounced')
    channel.discard()
    await vi.advanceTimersByTimeAsync(600)
    await channel.flush()

    expect(write).not.toHaveBeenCalled()
    expect(channel.getSnapshot()).toMatchObject({ phase: 'idle', dirtyKeys: [] })
  })
})
