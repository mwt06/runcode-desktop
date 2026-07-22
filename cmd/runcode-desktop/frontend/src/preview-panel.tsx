import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Markdown } from './markdown'
import { readArtifact, readArtifactBytes, openExternal, resolveArtifactPath, copyText, reviewEdit, errText, type EditDiff } from './bridge'
import { Icon } from './icons'
import { classifyPreview, previewSrc, artifactKindLabel, kindIcon, fileColor, filterFiles, buildFileTree, type FileNode } from './preview'
import { type PreviewTab, tabKey } from './preview-tabs'
import { basename } from './paths'

// fencedCode wraps source in a Markdown code fence long enough to survive any run
// of backticks in the source, so the shared Markdown renderer (rehype-highlight)
// highlights it — reusing the existing pipeline instead of a second highlighter.
function fencedCode(text: string, lang?: string): string {
  const longest = (text.match(/`+/g) || []).reduce((m, s) => Math.max(m, s.length), 0)
  const fence = '`'.repeat(Math.max(3, longest + 1))
  return `${fence}${lang ?? ''}\n${text}\n${fence}`
}

// --- Office viewers ------------------------------------------------------
// docx and pptx render imperatively into a host node (each library builds its own
// markup), so they share ImperativeDocView. The renderers are lazy-imported inside
// these load functions, keeping the heavy libraries (docx-preview; pptx-preview,
// which pulls in echarts) out of the main bundle — they download only when such a
// file is actually previewed. Module-level so their identity is stable across
// renders (they are effect dependencies).

async function renderDocx(buf: ArrayBuffer, host: HTMLDivElement) {
  const docx = await import('docx-preview')
  await docx.renderAsync(buf, host, host, {
    className: 'docx',
    inWrapper: true,
    breakPages: true,
    renderHeaders: true,
    renderFooters: true,
    experimental: true,
    useBase64URL: true,
  })
}

// ImperativeDocView fetches a workspace file's bytes and hands them to a renderer
// that draws into a host node, owning the loading / error shell and an
// open-externally fallback. It re-runs when the file or the refresh key changes and
// cancels cleanly so a stale render never lands in the wrong file's host.
function ImperativeDocView({ relPath, reloadKey, load, busyHint }: {
  relPath: string
  reloadKey: number
  load: (buf: ArrayBuffer, host: HTMLDivElement) => Promise<void>
  busyHint: string
}) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [err, setErr] = useState('')
  useEffect(() => {
    let cancelled = false
    setState('loading')
    setErr('')
    void (async () => {
      try {
        const buf = await readArtifactBytes(relPath)
        const host = hostRef.current
        if (cancelled || !host) return
        host.replaceChildren() // drop any prior file's render before drawing this one
        await load(buf, host)
        if (!cancelled) setState('ready')
      } catch (e) {
        if (!cancelled) {
          setErr(errText(e))
          setState('error')
        }
      }
    })()
    return () => { cancelled = true }
  }, [relPath, reloadKey, load])
  return (
    <div className="min-h-full bg-inset/40 flex flex-col items-center">
      {state === 'loading' && <div className="p-6 text-[13px] text-muted">正在加载{busyHint}预览…</div>}
      {state === 'error' && (
        <div className="p-6 text-[13px] text-red">预览失败：{err}
          <button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button>
        </div>
      )}
      {/* Host stays mounted (not hidden) so a renderer that measures its width — pptx —
          sees the real laid-out width rather than 0. flex + items-center 让固定宽度的
          docx 页面在面板里水平居中（窄于面板时居中，宽时正常横向滚动）。 */}
      <div ref={hostRef} className="docview-host" />
    </div>
  )
}

// XlsxView renders a workbook as one HTML table per sheet (SheetJS), with a sheet
// switcher when there is more than one. sheet_to_html HTML-escapes cell values, so
// the generated markup is safe to inject.
function XlsxView({ relPath, reloadKey }: { relPath: string; reloadKey: number }) {
  const [sheets, setSheets] = useState<{ name: string; html: string }[] | null>(null)
  const [active, setActive] = useState(0)
  const [err, setErr] = useState('')
  useEffect(() => {
    let cancelled = false
    setSheets(null)
    setErr('')
    setActive(0)
    void (async () => {
      try {
        const buf = await readArtifactBytes(relPath)
        const XLSX = await import('xlsx')
        if (cancelled) return
        const wb = XLSX.read(buf, { type: 'array' })
        const out = wb.SheetNames.map((name) => ({ name, html: XLSX.utils.sheet_to_html(wb.Sheets[name]) }))
        if (!cancelled) setSheets(out.length ? out : [{ name: 'Sheet1', html: '<em>空表</em>' }])
      } catch (e) {
        if (!cancelled) setErr(errText(e))
      }
    })()
    return () => { cancelled = true }
  }, [relPath, reloadKey])
  if (err) {
    return (
      <div className="p-6 text-[13px] text-red">预览失败：{err}
        <button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button>
      </div>
    )
  }
  if (!sheets) return <div className="p-6 text-[13px] text-muted">正在加载表格预览…</div>
  return (
    <div className="min-h-full flex flex-col">
      {sheets.length > 1 && (
        <div className="flex-none flex gap-1 flex-wrap px-3 py-2 border-b border-line2 bg-surface">
          {sheets.map((s, i) => (
            <button
              key={s.name}
              type="button"
              onClick={() => setActive(i)}
              className={`px-2.5 py-1 rounded-md text-[12px] ${i === active ? 'bg-primarysoft text-primaryink font-medium' : 'text-muted hover:bg-surface2'}`}
            >
              {s.name}
            </button>
          ))}
        </div>
      )}
      <div className="flex-1 overflow-auto p-3 xlsx-host" dangerouslySetInnerHTML={{ __html: sheets[active].html }} />
    </div>
  )
}

// PptxView presents a deck one slide at a time with left/right navigation and a page
// indicator. pptx-preview renders every slide stacked (list mode) into the host —
// including a hard-coded black wrapper background that styles.css overrides — and we
// turn that into a slideshow by showing only the active slide. It sizes each slide to
// the host width, deriving the height from the deck's real aspect ratio itself.
function PptxView({ relPath, reloadKey }: { relPath: string; reloadKey: number }) {
  const hostRef = useRef<HTMLDivElement | null>(null)
  const slidesRef = useRef<HTMLElement[]>([])
  const [state, setState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [err, setErr] = useState('')
  const [count, setCount] = useState(0)
  const [idx, setIdx] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState('loading')
    setErr('')
    setCount(0)
    setIdx(0)
    slidesRef.current = []
    void (async () => {
      try {
        const buf = await readArtifactBytes(relPath)
        const { init } = await import('pptx-preview')
        const host = hostRef.current
        if (cancelled || !host) return
        host.replaceChildren()
        const width = Math.max(360, Math.floor(host.clientWidth) || 900)
        const previewer = init(host, { width, mode: 'list' })
        await previewer.preview(buf)
        if (cancelled) return
        const slides = Array.from(host.querySelectorAll<HTMLElement>('.pptx-preview-slide-wrapper'))
        slides.forEach((el, i) => { el.style.display = i === 0 ? '' : 'none' })
        slidesRef.current = slides
        setCount(slides.length)
        setState('ready')
      } catch (e) {
        if (!cancelled) {
          setErr(errText(e))
          setState('error')
        }
      }
    })()
    return () => { cancelled = true }
  }, [relPath, reloadKey])

  // Navigation toggles DOM visibility directly (the slides are the library's nodes,
  // not React's) and mirrors the position into state for the indicator.
  const go = (to: number) => {
    const clamped = Math.max(0, Math.min(to, slidesRef.current.length - 1))
    slidesRef.current.forEach((el, i) => { el.style.display = i === clamped ? '' : 'none' })
    setIdx(clamped)
  }

  if (state === 'error') {
    return (
      <div className="p-6 text-[13px] text-red">预览失败：{err}
        <button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button>
      </div>
    )
  }
  return (
    <div
      className="relative flex flex-col h-full min-h-0 bg-inset/40 outline-none"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'ArrowLeft') { e.preventDefault(); go(idx - 1) }
        else if (e.key === 'ArrowRight') { e.preventDefault(); go(idx + 1) }
      }}
    >
      {state === 'loading' && <div className="p-6 text-[13px] text-muted">正在加载幻灯片预览…</div>}
      <div className="flex-1 min-h-0 overflow-auto p-3">
        {/* min-h-full + items-center 让幻灯片在面板里垂直居中；内容比面板高时容器随之
            撑高、从顶部正常滚动，不会裁掉顶部。水平居中由 pptx-host 的 margin:auto 负责。 */}
        <div className="min-h-full flex items-center justify-center">
          <div ref={hostRef} className="pptx-host w-full" />
        </div>
      </div>
      {state === 'ready' && count > 1 && (
        <>
          <button
            type="button" aria-label="上一页" title="上一页 (←)" disabled={idx === 0} onClick={() => go(idx - 1)}
            className="absolute left-2 top-1/2 -translate-y-1/2 w-9 h-9 rounded-full bg-surface/90 border border-line2 shadow-card flex items-center justify-center text-muted hover:text-ink disabled:opacity-30 disabled:cursor-default"
          >
            <Icon name="chevron-down" size={18} className="rotate-90" />
          </button>
          <button
            type="button" aria-label="下一页" title="下一页 (→)" disabled={idx === count - 1} onClick={() => go(idx + 1)}
            className="absolute right-2 top-1/2 -translate-y-1/2 w-9 h-9 rounded-full bg-surface/90 border border-line2 shadow-card flex items-center justify-center text-muted hover:text-ink disabled:opacity-30 disabled:cursor-default"
          >
            <Icon name="chevron-down" size={18} className="-rotate-90" />
          </button>
          <div className="absolute bottom-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-surface/90 border border-line2 shadow-card text-[12px] text-muted tabular-nums select-none">
            {idx + 1} / {count}
          </div>
        </>
      )}
    </div>
  )
}

// PreviewPanel renders one workspace artifact by type: HTML in a sandboxed iframe
// and images by the loopback static-server URL; Markdown/code/text by fetching the
// text via ReadArtifact and rendering it in React; Office docs (docx/pptx/xlsx) via
// lazy-loaded in-browser viewers; PDF by the WebView's native viewer.
function IconBtn({ name, title, onClick }: { name: string; title: string; onClick: () => void }) {
  return (
    <button title={title} onClick={onClick} className="flex-none w-7 h-7 flex items-center justify-center rounded-md text-muted hover:text-ink hover:bg-surface2">
      <Icon name={name} size={14} />
    </button>
  )
}

export function PreviewPanel({ baseURL, relPath, onClose }: { baseURL: string; relPath: string; onClose: () => void }) {
  const { kind, lang } = classifyPreview(relPath)
  const [bust, setBust] = useState(1)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState('')
  const name = basename(relPath)
  const textual = kind === 'markdown' || kind === 'code' || kind === 'text'

  useEffect(() => {
    if (!textual) return
    let ignore = false
    setText(null)
    setErr('')
    readArtifact(relPath)
      .then((t) => { if (!ignore) setText(t) })
      .catch((e) => { if (!ignore) setErr(errText(e)) })
    return () => { ignore = true }
  }, [relPath, kind, bust, textual])

  const copyPath = async () => {
    try { await copyText(await resolveArtifactPath(relPath)) } catch { /* non-fatal */ }
  }

  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <span className="flex-none" style={{ color: fileColor(relPath) }}><Icon name={kindIcon(kind)} size={15} /></span>
        <span className="flex-none text-[10.5px] text-faint bg-inset rounded px-1.5 py-0.5 mr-auto">{artifactKindLabel(kind)}</span>
        <IconBtn name="refresh" title="刷新" onClick={() => setBust((v) => v + 1)} />
        <IconBtn name="external-link" title="用系统默认程序打开" onClick={() => { openExternal(relPath).catch(() => {}) }} />
        <IconBtn name="copy" title="复制路径" onClick={copyPath} />
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto">
        {kind === 'html' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" sandbox="allow-scripts allow-forms allow-popups allow-modals" />
        )}
        {(kind === 'image' || kind === 'svg') && baseURL && (
          <div className="p-4 flex items-center justify-center min-h-full bg-inset/30"><img src={previewSrc(baseURL, relPath, bust)} alt={name} className="max-w-full" /></div>
        )}
        {kind === 'markdown' && text != null && <div className="p-4"><Markdown>{text}</Markdown></div>}
        {kind === 'code' && text != null && <div className="p-4"><Markdown>{fencedCode(text, lang)}</Markdown></div>}
        {kind === 'text' && text != null && (
          <pre className="m-0 p-4 font-mono text-[12.5px] leading-[1.6] whitespace-pre-wrap break-words">{text}</pre>
        )}
        {kind === 'docx' && <ImperativeDocView relPath={relPath} reloadKey={bust} load={renderDocx} busyHint=" Word" />}
        {kind === 'pptx' && <PptxView relPath={relPath} reloadKey={bust} />}
        {kind === 'xlsx' && <XlsxView relPath={relPath} reloadKey={bust} />}
        {kind === 'pdf' && baseURL && (
          <iframe title={name} src={previewSrc(baseURL, relPath, bust)} className="w-full h-full border-0 bg-white" />
        )}
        {kind === 'pdf' && !baseURL && <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>}
        {kind === 'unsupported' && (
          <div className="p-6 text-[13px] text-muted">该文件类型暂不支持预览。<button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button></div>
        )}
        {(kind === 'html' || kind === 'image' || kind === 'svg') && !baseURL && (
          <div className="p-6 text-[13px] text-muted">预览服务不可用。</div>
        )}
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
      </div>
    </div>
  )
}

// DiffPanel renders the red/green review of one edit (baseline vs the turn's latest
// content), fetched via ReviewEdit. Reuses the diff-line CSS classes (.cl.diff_*).
function DiffPanel({ snapshotId, relPath, onClose }: { snapshotId: string; relPath: string; onClose: () => void }) {
  const [diff, setDiff] = useState<EditDiff | null>(null)
  const [err, setErr] = useState('')
  const name = basename(relPath)
  useEffect(() => {
    let ignore = false
    setDiff(null)
    setErr('')
    reviewEdit(snapshotId)
      .then((d) => { if (!ignore) setDiff(d) })
      .catch((e) => { if (!ignore) setErr(errText(e)) })
    return () => { ignore = true }
  }, [snapshotId])
  return (
    <div className="flex flex-col h-full min-h-0 bg-surface">
      <div className="flex-none flex items-center gap-1.5 h-[44px] px-2.5 border-b border-line2">
        <Icon name="diff" size={15} className="flex-none text-muted" />
        <span className="flex-none text-[10.5px] text-faint bg-inset rounded px-1.5 py-0.5 mr-auto">审核 · {name}</span>
        <IconBtn name="win-close" title="关闭" onClick={onClose} />
      </div>
      <div className="flex-1 min-h-0 overflow-auto py-2 font-mono text-[12.5px] leading-[1.6]">
        {err && <div className="p-6 text-[13px] text-red">{err}</div>}
        {diff && (diff.lines ?? []).length === 0 && <div className="p-6 text-[13px] text-muted">无差异。</div>}
        {diff && (diff.lines ?? []).map((l, i) => (
          <div key={i} className={(l.stream || '').startsWith('diff') ? `cl ${l.stream}` : 'px-2.5 whitespace-pre text-muted'}>{l.text}</div>
        ))}
      </div>
    </div>
  )
}

// PreviewTabs is the tab strip above the preview: one tab per open file/diff. Lines
// are dropped; the active tab reads as a soft rounded background pill. Each file tab
// carries its color-coded type icon (diff tabs use a neutral diff glyph).
export function PreviewTabs({ tabs, active, onSelect, onClose }: { tabs: PreviewTab[]; active: string | null; onSelect: (key: string) => void; onClose: (key: string) => void }) {
  return (
    <div className="flex-none flex items-center gap-0.5 h-[38px] px-1.5 overflow-x-auto bg-surface">
      {tabs.map((t) => {
        const key = tabKey(t)
        const name = basename(t.relPath)
        const on = key === active
        const iconName = t.kind === 'diff' ? 'diff' : kindIcon(classifyPreview(t.relPath).kind)
        const iconColor = t.kind === 'diff' ? undefined : fileColor(t.relPath)
        return (
          <div
            key={key}
            onClick={() => onSelect(key)}
            title={t.relPath}
            className={`group flex items-center gap-1.5 pl-2 pr-1.5 h-[28px] max-w-[190px] flex-none cursor-pointer rounded-md text-[12.5px] ${on ? 'bg-surface2 text-ink' : 'text-muted hover:bg-surface2/60 hover:text-ink'}`}
          >
            <span className="flex-none" style={iconColor ? { color: iconColor } : undefined}><Icon name={iconName} size={13} /></span>
            <span className="truncate">{name}</span>
            <button
              className="flex-none w-4 h-4 flex items-center justify-center rounded opacity-0 group-hover:opacity-100 hover:bg-line2 hover:text-ink"
              onClick={(e) => { e.stopPropagation(); onClose(key) }}
            >
              <Icon name="win-close" size={10} />
            </button>
          </div>
        )
      })}
    </div>
  )
}

// PreviewPane composes the tab strip with the active tab's panel (file or diff).
export function PreviewPane({ tabs, active, baseURL, onSelect, onClose, onCloseTab }: { tabs: PreviewTab[]; active: string | null; baseURL: string; onSelect: (key: string) => void; onClose: () => void; onCloseTab: (key: string) => void }) {
  const activeTab = tabs.find((t) => tabKey(t) === active) ?? null
  return (
    <div className="flex flex-col h-full min-h-0">
      <PreviewTabs tabs={tabs} active={active} onSelect={onSelect} onClose={onCloseTab} />
      <div className="flex-1 min-h-0">
        {activeTab?.kind === 'file' && <PreviewPanel key={active} baseURL={baseURL} relPath={activeTab.relPath} onClose={onClose} />}
        {activeTab?.kind === 'diff' && <DiffPanel key={active} snapshotId={activeTab.snapshotId} relPath={activeTab.relPath} onClose={onClose} />}
      </div>
    </div>
  )
}

// FileBrowser lists the workspace as a shallow read-only tree; clicking a file
// asks to preview it (the parent decides if it is previewable).
export function FileBrowser({ files, onPick, autoOpen, onToggleAutoOpen }: { files: string[]; onPick: (relPath: string) => void; autoOpen?: boolean; onToggleAutoOpen?: () => void }) {
  const [query, setQuery] = useState('')
  const tree = buildFileTree(filterFiles(files, query))
  const render = (nodes: FileNode[], depth: number): ReactNode =>
    nodes.map((n) =>
      n.dir ? (
        <div key={n.path}>
          <div className="px-2 py-1 text-[12.5px] text-muted font-medium" style={{ paddingLeft: 8 + depth * 12 }}>{n.name}/</div>
          {n.children && render(n.children, depth + 1)}
        </div>
      ) : (
        <div key={n.path} onClick={() => onPick(n.path)} className="flex items-center gap-1.5 px-2 py-1 text-[12.5px] text-ink hover:bg-surface2 cursor-pointer" style={{ paddingLeft: 8 + depth * 12 }} title={n.path}>
          <span className="flex-none" style={{ color: fileColor(n.path) }}><Icon name={kindIcon(classifyPreview(n.path).kind)} size={13} /></span>
          <span className="truncate">{n.name}</span>
        </div>
      ),
    )
  return (
    <div className="flex flex-col h-full min-h-0">
      <div className="flex-none p-2 border-b border-line2">
        {onToggleAutoOpen && (
          <label className="flex items-center gap-1.5 mb-2 text-[12px] text-muted cursor-pointer select-none">
            <input type="checkbox" checked={!!autoOpen} onChange={onToggleAutoOpen} />
            写完自动预览
          </label>
        )}
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="筛选文件…"
          className="w-full text-[12.5px] bg-inset rounded-md px-2.5 py-1.5 outline-none text-ink placeholder:text-faint"
        />
      </div>
      <div className="flex-1 min-h-0 text-[12.5px] py-1 overflow-auto">{render(tree, 0)}</div>
    </div>
  )
}
