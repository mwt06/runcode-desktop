// 栅格生图专用单实例查看 host：同步占用、ownerToken 互斥；不接普通文件／InlinePreview。
import {
  createContext, useCallback, useContext, useEffect, useMemo, useRef, useState,
  type ReactNode,
} from 'react'
import { openArtifact, releaseArtifact, type ArtifactReference, type ImageGenerationArtifact } from '@/core/bridge'

export type GeneratedImageViewerTarget = {
  sessionId: string
  sessionGeneration: string
  toolUseId: string
  image: ImageGenerationArtifact
  alt: string
}

export type GeneratedImageViewerMode = 'view' | 'annotate'

type OpenHandle = { ownerToken: string }

type ViewerState = {
  active: boolean
  suspended: boolean
  mode: GeneratedImageViewerMode
  target: GeneratedImageViewerTarget | null
  ownerToken: string | null
  artifact: ArtifactReference | null
  trigger: HTMLElement | null
  confirmDiscard: boolean
}

type Api = {
  active: boolean
  suspended: boolean
  mode: GeneratedImageViewerMode
  target: GeneratedImageViewerTarget | null
  artifact: ArtifactReference | null
  confirmDiscard: boolean
  tryOpen: (target: GeneratedImageViewerTarget, mode: GeneratedImageViewerMode, trigger: HTMLElement | null) => OpenHandle | null
  setMode: (mode: GeneratedImageViewerMode) => void
  requestClose: () => void
  confirmDiscardClose: () => void
  cancelDiscardClose: () => void
  closeIfCurrent: (ownerToken: string) => void
  markDirty: (dirty: boolean) => void
}

const Ctx = createContext<Api | null>(null)

let tokenSeq = 0
const nextToken = () => `giv-${++tokenSeq}-${Date.now()}`

export function GeneratedImageViewerProvider({
  suspended = false,
  children,
}: {
  suspended?: boolean
  children: ReactNode
}) {
  const [state, setState] = useState<ViewerState>({
    active: false, suspended: false, mode: 'view', target: null, ownerToken: null,
    artifact: null, trigger: null, confirmDiscard: false,
  })
  const ownerRef = useRef<string | null>(null)
  const artifactRef = useRef<ArtifactReference | null>(null)
  const dirtyRef = useRef(false)
  const triggerRef = useRef<HTMLElement | null>(null)

  const releaseCurrent = useCallback(() => {
    const art = artifactRef.current
    artifactRef.current = null
    if (art?.reference) void releaseArtifact(art.reference).catch(() => {})
  }, [])

  const hardClose = useCallback(() => {
    const trigger = triggerRef.current
    releaseCurrent()
    ownerRef.current = null
    dirtyRef.current = false
    triggerRef.current = null
    setState({
      active: false, suspended: false, mode: 'view', target: null, ownerToken: null,
      artifact: null, trigger: null, confirmDiscard: false,
    })
    requestAnimationFrame(() => {
      if (trigger && document.contains(trigger)) trigger.focus()
    })
  }, [releaseCurrent])

  const tryOpen = useCallback((target: GeneratedImageViewerTarget, mode: GeneratedImageViewerMode, trigger: HTMLElement | null): OpenHandle | null => {
    // 同步占用：打开中的 RPC 也算已占用。
    if (ownerRef.current) return null
    const ownerToken = nextToken()
    ownerRef.current = ownerToken
    dirtyRef.current = false
    triggerRef.current = trigger
    setState({
      active: true, suspended: false, mode, target, ownerToken,
      artifact: null, trigger, confirmDiscard: false,
    })
    void openArtifact(target.sessionId, target.image.path)
      .then((artifact) => {
        if (ownerRef.current !== ownerToken) {
          void releaseArtifact(artifact.reference).catch(() => {})
          return
        }
        if (target.sessionGeneration && artifact.generation && artifact.generation !== target.sessionGeneration) {
          void releaseArtifact(artifact.reference).catch(() => {})
          hardClose()
          return
        }
        artifactRef.current = artifact
        setState((s) => (s.ownerToken === ownerToken ? { ...s, artifact } : s))
      })
      .catch(() => {
        if (ownerRef.current === ownerToken) hardClose()
      })
    return { ownerToken }
  }, [hardClose])

  const requestClose = useCallback(() => {
    if (!ownerRef.current) return
    if (dirtyRef.current) {
      setState((s) => ({ ...s, confirmDiscard: true }))
      return
    }
    hardClose()
  }, [hardClose])

  const closeIfCurrent = useCallback((ownerToken: string) => {
    if (ownerRef.current !== ownerToken) return
    hardClose()
  }, [hardClose])

  const setMode = useCallback((mode: GeneratedImageViewerMode) => {
    setState((s) => (s.active ? { ...s, mode, confirmDiscard: false } : s))
  }, [])

  const markDirty = useCallback((dirty: boolean) => {
    dirtyRef.current = dirty
  }, [])

  useEffect(() => {
    setState((s) => (s.active ? { ...s, suspended } : s))
  }, [suspended])

  useEffect(() => () => { releaseCurrent(); ownerRef.current = null }, [releaseCurrent])

  const api = useMemo<Api>(() => ({
    active: state.active,
    suspended: state.suspended || suspended,
    mode: state.mode,
    target: state.target,
    artifact: state.artifact,
    confirmDiscard: state.confirmDiscard,
    tryOpen,
    setMode,
    requestClose,
    confirmDiscardClose: hardClose,
    cancelDiscardClose: () => setState((s) => ({ ...s, confirmDiscard: false })),
    closeIfCurrent,
    markDirty,
  }), [state, suspended, tryOpen, setMode, requestClose, hardClose, closeIfCurrent, markDirty])

  return <Ctx.Provider value={api}>{children}</Ctx.Provider>
}

export function useGeneratedImageViewer(): Api {
  const ctx = useContext(Ctx)
  if (!ctx) throw new Error('useGeneratedImageViewer requires GeneratedImageViewerProvider')
  return ctx
}

/** 可选：预览卡在无 Provider 时静默跳过（开发画廊）。 */
export function useGeneratedImageViewerOptional(): Api | null {
  return useContext(Ctx)
}
