// 预览标签携带来源会话与后端引用；路径仅用于展示，不从当前焦点猜读取权限。
import { useEffect, useRef, useState, type PointerEvent } from 'react'
import { usePersistentBool, usePersistentNumber } from '@/hooks/use-persistent-state'
import { copyText, errText, Events, onEvent, openArtifact, openExternal, releaseArtifact, revealInFolder, resolveArtifactPath, type ArtifactReference } from '@/core/bridge'
import { createActionScope } from './action-scope'
import type { ArtifactActions } from '@/chat/artifact-actions'
import { clampPreviewWidth, previewWidthBounds } from '@/preview/classify'
import type { CodePreviewTarget } from '@/preview/code-preview'
import { closeTab, openTab, tabKey, type PreviewTab } from '@/preview/tabs'

export type PreviewPanelState = ReturnType<typeof usePreviewPanel>

// 标签页按会话存。宽度、自动预览开关、浏览器开合是纯界面偏好，几条会话共用。
//
// 文件引用与 diff 快照分别绑定来源实例/会话；切换焦点保留各自标签，不借用另一条会话。
const EMPTY_TABS: { tabs: PreviewTab[]; active: string | null } = { tabs: [], active: null }

export function usePreviewPanel(sessionID: string, notify: (text: string) => void = () => {}) {
  const scope = useRef(createActionScope())
  scope.current.focus(sessionID)
  const owner = useRef(sessionID)
  owner.current = sessionID
  const lifecycle = useRef(new Map<string, { blocked: boolean; generation?: string }>())
  useEffect(() => { const active = scope.current; active.activate(); return () => active.deactivate() }, [])
  const report = useRef(notify)
  report.current = notify
  const [bySession, setBySession] = useState<Record<string, { tabs: PreviewTab[]; active: string | null }>>({})
  useEffect(() => onEvent(Events.PreviewLifecycle, ({ sessionId, phase, generation }) => {
    if (!sessionId || !['invalidating', 'ready', 'closed'].includes(phase)) return
    const previous = lifecycle.current.get(sessionId)
    if (phase === 'ready' && previous && previous.generation === generation && !previous.blocked) return
    lifecycle.current.set(sessionId, { blocked: phase !== 'ready', generation })
    if (owner.current === sessionId) scope.current.invalidate()
    // A rebuilt host cannot honor old capability handles. Close its tabs so a
    // fresh user action obtains references from the new generation.
    setBySession((all) => {
      if (!(sessionId in all)) return all
      const next = { ...all }
      delete next[sessionId]
      return next
    })
  }), [])
  const references = useRef(new Set<string>())
  useEffect(() => {
    const next = new Set(Object.values(bySession).flatMap((s) => s.tabs.flatMap((t) => t.kind === 'file' && t.artifact ? [t.artifact.reference] : [])))
    for (const ref of references.current) if (!next.has(ref)) void releaseArtifact(ref).catch(() => {})
    references.current = next
  }, [bySession])
  useEffect(() => () => { for (const ref of references.current) void releaseArtifact(ref).catch(() => {}); references.current.clear() }, [])
  const cur = bySession[sessionID] ?? EMPTY_TABS
  const tabs = cur.tabs
  const active = cur.active
  const patch = (next: { tabs: PreviewTab[]; active: string | null }) =>
    setBySession((m) => (sessionID ? { ...m, [sessionID]: next } : m))
  const setActive = (key: string | null) => patch({ tabs, active: key })
  const [browseOpen, setBrowseOpen] = useState(false)
  const [autoOpen, toggleAutoOpen] = usePersistentBool('preview.autoOpen', true)
  const [width, setWidth, commitWidth] = usePersistentNumber('preview.width', (stored) =>
    clampPreviewWidth(stored, window.innerWidth),
  )

  // 窗口变窄时预览栏要跟着让位。只在挂载那一次 clamp 不够：把窗口调小之后（麒麟上
  // 还要再除以整页缩放的倍数），可用的 CSS 宽度缩了，而面板宽度还是上一次的值，
  // 对话栏就被挤到 CHAT_MIN 以下——每行只剩一两个字、输入框被挤没。
  //
  // wanted 记的是"用户想要多宽"，窗口重新变宽时还回去；只按当前窗口 clamp 的话，
  // 缩一次就永久变窄了，而人并没有调过它。
  const wanted = useRef(width)
  useEffect(() => {
    const onResize = () => {
      const { min, max } = previewWidthBounds(window.innerWidth)
      setWidth(Math.min(Math.max(wanted.current, min), max))
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [setWidth])

  // opens counts every preview opened, from any source — the model's open_preview,
  // an artifact card, the file browser. Auto-preview reads it to tell "nothing has
  // been shown this turn" from "something already has", so it never yanks the panel
  // away from what the user (or the model) deliberately put there. A ref, not
  // state: the reader only samples it at turn boundaries, and a counter that
  // re-rendered the whole app on every open would be pure cost.
  const opens = useRef(0)

  const open = (tab: PreviewTab) => {
    opens.current++
    setBySession((m) => {
      const cur = m[sessionID] ?? EMPTY_TABS
      const r = openTab(cur.tabs, cur.active, tab)
      return { ...m, [sessionID]: r }
    })
    setBrowseOpen(false)
  }
  const openReference = (artifact: ArtifactReference) => {
    const state = lifecycle.current.get(sessionID)
    if (owner.current !== sessionID || artifact.sessionId !== sessionID || state?.blocked || (state?.generation && artifact.generation !== state.generation)) { void releaseArtifact(artifact.reference).catch(() => {}); return false }
    open({ kind: 'file', relPath: artifact.path, artifact })
    return true
  }
  const openFile = async (path: string, quiet = false): Promise<boolean> => {
    if (lifecycle.current.get(sessionID)?.blocked) return false
    const current = scope.current.capture()
    try {
      const artifact = await openArtifact(sessionID, path)
      if (!current() || artifact.sessionId !== sessionID) { void releaseArtifact(artifact.reference).catch(() => {}); return false }
      return openReference(artifact)
    } catch (e) { if (current() && !quiet) report.current(errText(e)); return false }
  }
  const actions: ArtifactActions = {
    external: (p) => act(p, openExternal),
    reveal: (p) => act(p, revealInFolder),
    copyPath: (p) => act(p, async (r) => copyText(await resolveArtifactPath(r))),
  }
  async function act(path: string, perform: (ref: string) => Promise<void>) {
    if (lifecycle.current.get(sessionID)?.blocked) return
    const current = scope.current.capture()
    let reference = ''
    try {
      const artifact = await openArtifact(sessionID, path)
      reference = artifact.reference
      if (current()) await perform(reference)
    } catch (e) { if (current()) report.current(errText(e)) }
    finally { if (reference) void releaseArtifact(reference).catch(() => {}) }
  }
  const openCodePreview = (target: CodePreviewTarget) => {
    if (target.kind === 'file') {
      void openFile(target.relPath)
      return
    }
    if (lifecycle.current.get(sessionID)?.blocked) return
    opens.current++
    setBySession((all) => {
      const cur = all[sessionID] ?? EMPTY_TABS
      const tab: PreviewTab = {
        kind: 'inline-static',
        ownerKey: target.ownerKey,
        title: target.title,
        sourceKind: target.sourceKind,
        source: target.source,
        messageID: target.messageId,
        sessionID,
      }
      const opened = openTab(cur.tabs, cur.active, tab)
      let tabs = opened.tabs
      let active: string | null = opened.active
      let inlineCount = tabs.filter((t) => t.kind === 'inline-static').length
      while (inlineCount > 8) {
        const oldest = tabs.findIndex((t) => t.kind === 'inline-static' && tabKey(t) !== active)
        if (oldest < 0) break
        tabs = tabs.filter((_, i) => i !== oldest)
        inlineCount--
      }
      if (active && !tabs.some((t) => tabKey(t) === active)) active = tabs[tabs.length - 1] ? tabKey(tabs[tabs.length - 1]) : null
      return { ...all, [sessionID]: { tabs, active } }
    })
    setBrowseOpen(false)
  }
  const openDiff = (snapshotId: string, relPath: string) => open({ kind: 'diff', snapshotId, relPath, sessionID })
  const close = (key: string) => {
    patch(closeTab(tabs, active, key))
  }
  // closeAll 清掉**聚焦这条会话**的标签，别人的不动——它对应面板上的「关闭全部」。
  //
  // 它一度是清掉所有会话的：那时"换工作区"意味着关掉当前会话、在新目录重开一条，
  // 而标签存的是工作区相对路径与编辑快照 id，换了目录就全成了坏引用，所以一把清空。
  // 多工作区并行之后这个前提没了——换目录是**加开**一条会话（OpenSession(workspace)），
  // 留在旧目录的那几条还开着，它们的标签依然有效。会话与目录一一对应且终生不变，
  // 于是"标签失效"这件事只会随会话一起消失（dropSession），不再需要全局清空。
  const closeAll = () => { patch({ tabs: [], active: null }) }
  // dropSession 丢掉一条会话的标签——那条会话被关掉时。
  const dropSession = (id: string) =>
    setBySession((m) => {
      if (!(id in m)) return m
      const next = { ...m }
      delete next[id]
      return next
    })

  // 切换焦点只收起文件浏览器；各会话标签保留，迟到结果由 action-scope 丢弃。
  const focused = useRef(sessionID)
  useEffect(() => {
    if (focused.current === sessionID) return
    focused.current = sessionID
    setBrowseOpen(false)
  }, [sessionID])

  // 拖左边缘调宽：按下记起点，移动时按位移换算(向左拖变宽)，松手才落盘。
  const drag = useRef<{ startX: number; startW: number } | null>(null)
  const dragHandlers = {
    onPointerDown: (e: PointerEvent) => {
      drag.current = { startX: e.clientX, startW: width }
      ;(e.target as HTMLElement).setPointerCapture(e.pointerId)
    },
    onPointerMove: (e: PointerEvent) => {
      if (!drag.current) return
      const dx = drag.current.startX - e.clientX // dragging the left edge leftward grows the pane
      // 手动拖也守同一条底线：对话栏不能被挤到 CHAT_MIN 以下（见 previewWidthBounds）。
      const { min, max } = previewWidthBounds(window.innerWidth)
      const next = Math.min(Math.max(drag.current.startW + dx, min), max)
      wanted.current = next
      setWidth(next)
    },
    onPointerUp: () => {
      if (drag.current) {
        commitWidth()
        drag.current = null
      }
    },
  }

  return {
    tabs, active, setActive, browseOpen, setBrowseOpen,
    autoOpen, toggleAutoOpen, width, dragHandlers, opens,
    openFile, openReference, openCodePreview, openDiff, actions, close, closeAll, dropSession,
  }
}
