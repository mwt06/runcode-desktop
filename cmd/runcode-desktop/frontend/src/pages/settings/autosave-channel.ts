export type AutoSavePhase = 'idle' | 'dirty' | 'saving' | 'saved' | 'invalid' | 'error'

export type AutoSaveSnapshot<T extends object> = {
  draft: T
  confirmed: T
  dirtyKeys: readonly (keyof T)[]
  phase: AutoSavePhase
  error: string
  revision: number
}

export type AutoSaveMode = 'immediate' | 'debounced' | 'deferred'

export type AutoSaveChannel<T extends object> = {
  getSnapshot(): AutoSaveSnapshot<T>
  subscribe(listener: () => void): () => void
  edit(next: T, mode: AutoSaveMode): void
  rebase(authoritative: T): void
  discard(): void
  flush(): Promise<boolean>
  setReady(ready: boolean): void
}

type Options<T extends object> = {
  initial: T
  write: (value: T) => Promise<T>
  validate?: (value: T) => string | null
  delayMs: number
}

function sameValue(a: unknown, b: unknown): boolean {
  return Object.is(a, b)
}

function dirtyKeys<T extends object>(draft: T, confirmed: T): (keyof T)[] {
  return (Object.keys(draft) as (keyof T)[]).filter((key) => !sameValue(draft[key], confirmed[key]))
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

export function createAutoSaveChannel<T extends object>({
  initial,
  write,
  validate,
  delayMs,
}: Options<T>): AutoSaveChannel<T> {
  let state: AutoSaveSnapshot<T> = {
    draft: initial,
    confirmed: initial,
    dirtyKeys: [],
    phase: 'idle',
    error: '',
    revision: 0,
  }
  let ready = true
  let timer: ReturnType<typeof setTimeout> | null = null
  let inFlight: Promise<boolean> | null = null
  // 外部权威回填会推进这个序号；旧写入的回包不能覆盖更新后的 confirmed。
  let authorityRevision = 0
  const listeners = new Set<() => void>()

  const emit = () => {
    for (const listener of listeners) listener()
  }

  const update = (next: AutoSaveSnapshot<T>) => {
    state = next
    emit()
  }

  const clearTimer = () => {
    if (timer === null) return
    clearTimeout(timer)
    timer = null
  }

  const run = async (): Promise<boolean> => {
    while (ready) {
      const keys = dirtyKeys(state.draft, state.confirmed)
      if (keys.length === 0) {
        if (state.phase !== 'error') update({ ...state, dirtyKeys: [], phase: 'idle', error: '' })
        return true
      }
      if (validate) {
        const invalid = validate(state.draft)
        if (invalid) {
          update({ ...state, dirtyKeys: keys, phase: 'invalid', error: invalid })
          return false
        }
      }

      const snapshot = state.draft
      const writeRevision = state.revision
      const writeAuthorityRevision = authorityRevision
      update({ ...state, dirtyKeys: keys, phase: 'saving', error: '' })
      let confirmed: T
      try {
        confirmed = await write(snapshot)
      } catch (error) {
        update({
          ...state,
          dirtyKeys: dirtyKeys(state.draft, state.confirmed),
          phase: 'error',
          error: errorMessage(error),
        })
        return false
      }

      if (writeAuthorityRevision !== authorityRevision) {
        // 写入期间已有更新的权威回填；保留新 confirmed，避免旧回包把它撤回。
        const remaining = dirtyKeys(state.draft, state.confirmed)
        update({
          ...state,
          dirtyKeys: remaining,
          phase: remaining.length > 0 ? 'dirty' : 'idle',
          error: '',
        })
        continue
      }

      const currentDraft = state.draft
      const mergedDraft = Object.fromEntries(
        (Object.keys(currentDraft) as (keyof T)[]).map((key) => [
          key,
          state.revision === writeRevision || sameValue(currentDraft[key], snapshot[key])
            ? confirmed[key]
            : currentDraft[key],
        ]),
      ) as T
      const remaining = dirtyKeys(mergedDraft, confirmed)
      update({
        draft: mergedDraft,
        confirmed,
        dirtyKeys: remaining,
        phase: remaining.length > 0 ? 'dirty' : 'saved',
        error: '',
        revision: state.revision,
      })
      if (remaining.length === 0) return true
    }
    return false
  }

  const flush = (): Promise<boolean> => {
    clearTimer()
    if (inFlight) return inFlight
    if (!ready) return Promise.resolve(false)
    inFlight = run().finally(() => {
      inFlight = null
    })
    return inFlight
  }

  return {
    getSnapshot: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    edit(next, mode) {
      clearTimer()
      const keys = dirtyKeys(next, state.confirmed)
      update({
        ...state,
        draft: next,
        dirtyKeys: keys,
        phase: keys.length === 0 ? 'idle' : 'dirty',
        error: '',
        revision: state.revision + 1,
      })
      if (keys.length === 0) return
      if (mode === 'immediate') queueMicrotask(() => void flush())
      if (mode === 'debounced') timer = setTimeout(() => void flush(), delayMs)
    },
    rebase(authoritative) {
      const current = state.draft
      const keys = dirtyKeys(current, state.confirmed)
      const nextDraft = Object.fromEntries(
        (Object.keys(current) as (keyof T)[]).map((key) => [
          key,
          keys.includes(key) ? current[key] : authoritative[key],
        ]),
      ) as T
      authorityRevision += 1
      const remaining = dirtyKeys(nextDraft, authoritative)
      update({
        ...state,
        draft: nextDraft,
        confirmed: authoritative,
        dirtyKeys: remaining,
        phase: remaining.length > 0 ? 'dirty' : 'idle',
        error: '',
      })
    },
    discard() {
      clearTimer()
      update({
        ...state,
        confirmed: state.draft,
        dirtyKeys: [],
        phase: 'idle',
        error: '',
      })
    },
    flush,
    setReady(next) {
      ready = next
      if (next && dirtyKeys(state.draft, state.confirmed).length > 0) void flush()
    },
  }
}
