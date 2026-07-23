// 一次工具调用展开后的详情视图：入参(按工具定制的表格/命令块，兜底折叠 JSON)
// 与返回内容(匹配文件树 / 图片 / 输出行)。被顶层执行卡与子代理卡共用。
import { useMemo, useState, type ReactNode } from 'react'
import { Icon } from '@/ui/icons'
import { type ToolEvent } from '@/core/bridge'
import { useStickToBottom } from '@/hooks/use-stick-to-bottom'
import { buildFileTree, classifyPreview, fileColor, kindIcon, type FileNode } from '@/preview/classify'
import { formatInput, lineClass, toolInputObj } from './tool-text'

// kvRows renders a compact key→value table for a tool's structured input.
function kvRows(rows: ([string, unknown] | false | null | undefined)[]) {
  const pairs = rows.filter(Boolean) as [string, unknown][]
  return (
    <div className="flex flex-col gap-1.5 bg-surface2 border border-line rounded-[8px] py-2 px-2.5">
      {pairs.map(([k, v], i) => (
        <div key={i} className="flex gap-2.5 text-[12.5px] min-w-0">
          <span className="text-faint flex-none w-[48px]">{k}</span>
          <span className="font-mono text-ink break-all min-w-0">{String(v ?? '')}</span>
        </div>
      ))}
    </div>
  )
}

// RawJson shows a tool's raw arguments as pretty JSON, collapsed by default when
// long so a big payload does not flood the card.
function RawJson({ value }: { value: unknown }) {
  const text = formatInput(value)
  const [open, setOpen] = useState(text.length <= 400)
  if (!text || text === '{}') return <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无入参）</div>
  return (
    <div>
      {text.length > 400 && (
        <button onClick={() => setOpen((v) => !v)} className="text-[12px] text-muted hover:text-ink inline-flex items-center gap-1 mb-1.5">
          <Icon name="chevron-down" size={13} className={open ? 'rotate-180 transition' : 'transition'} />
          {open ? '收起原始入参' : `展开原始入参 · ${text.length} 字符`}
        </button>
      )}
      {open && (
        <pre className="m-0 font-mono text-[12px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[200px] overflow-auto whitespace-pre-wrap break-all">
          {text.length > 16000 ? text.slice(0, 16000) + '\n… 已截断' : text}
        </pre>
      )}
    </div>
  )
}

// ToolInputView renders a tool's input in a clean, tool-specific shape — a command
// block, a key/value table, etc. — falling back to collapsible raw JSON for tools
// without a tailored view.
function ToolInputView({ tool }: { tool: ToolEvent }) {
  const o = toolInputObj(tool)
  const s = (k: string) => (o[k] != null ? String(o[k]) : '')
  switch (tool.toolName) {
    case 'Bash':
      if (s('command')) return (
        <div>
          <pre className="m-0 font-mono text-[12.5px] leading-[1.5] bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[160px] overflow-auto whitespace-pre-wrap break-all text-ink">{s('command')}</pre>
          {(o.timeout || o.run_in_background) ? <div className="text-[11.5px] text-faint mt-1">{o.timeout ? `超时 ${s('timeout')}ms` : ''}{o.run_in_background ? (o.timeout ? ' · ' : '') + '后台运行' : ''}</div> : null}
        </div>
      )
      break
    case 'Read':
      if (s('path')) return kvRows([['路径', s('path')], !!(o.offset || o.limit) && ['范围', `第 ${o.offset ?? 0} 行起${o.limit ? `，≤ ${s('limit')} 行` : ''}`], !!o.pages && ['页', s('pages')]])
      break
    case 'Grep':
      if (s('pattern')) return kvRows([['模式', s('pattern')], !!o.path && ['路径', s('path')], !!o.glob && ['glob', s('glob')], !!o.type && ['类型', s('type')], !!o.output_mode && ['输出', s('output_mode')]])
      break
    case 'Glob':
      if (s('pattern')) return kvRows([['模式', s('pattern')], !!o.path && ['路径', s('path')]])
      break
    case 'Write':
    case 'Edit':
    case 'Delete':
      if (s('path')) return kvRows([['路径', s('path')], tool.toolName === 'Delete' && !!o.permanent && ['方式', '永久删除']])
      break
    case 'WebFetch':
      if (s('url')) return kvRows([['URL', s('url')]])
      break
  }
  return <RawJson value={o} />
}

// MatchedFileTree renders a Glob/Grep matched-file list as a collapsible
// directory tree instead of one full path per line: each directory appears once
// (a single-child directory chain collapses into one "a/b/c/" row) with a
// chevron + descendant-file count, and clicking it folds that subtree away —
// a big same-directory result set neither repeats its prefix nor swamps the
// card. Everything starts expanded; the fold state lives with the card.
function MatchedFileTree({ paths }: { paths: string[] }) {
  const tree = useMemo(() => buildFileTree(paths), [paths])
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const toggle = (p: string) =>
    setCollapsed((s) => {
      const next = new Set(s)
      if (next.has(p)) next.delete(p)
      else next.add(p)
      return next
    })
  const countFiles = (n: FileNode): number =>
    n.dir ? (n.children ?? []).reduce((sum, c) => sum + countFiles(c), 0) : 1
  const render = (nodes: FileNode[], depth: number): ReactNode[] =>
    nodes.flatMap((n) => {
      const pad = { paddingLeft: depth * 14 }
      if (!n.dir) {
        return [
          <div key={'f:' + n.path} className="flex items-center gap-1.5 min-w-0" style={pad} title={n.path}>
            <span className="flex-none" style={{ color: fileColor(n.path) }}><Icon name={kindIcon(classifyPreview(n.path).kind)} size={13} /></span>
            <span className="font-mono text-[12px] text-ink truncate">{n.name}</span>
          </div>,
        ]
      }
      let label = n.name
      let cur = n
      while ((cur.children?.length ?? 0) === 1 && cur.children![0].dir) {
        cur = cur.children![0]
        label += '/' + cur.name
      }
      const open = !collapsed.has(n.path)
      return [
        <div
          key={'d:' + n.path}
          onClick={() => toggle(n.path)}
          className="flex items-center gap-1.5 cursor-pointer select-none"
          style={pad}
          title={cur.path}
        >
          <Icon name="chevron-down" size={11} className={`flex-none text-faint transition ${open ? '' : '-rotate-90'}`} />
          <Icon name="folder" size={13} className="flex-none text-faint" />
          <span className="font-mono text-[12px] text-ink truncate">{label}/</span>
          <span className="font-mono text-[11px] text-faint flex-none">· {countFiles(cur)}</span>
        </div>,
        ...(open ? render(cur.children ?? [], depth + 1) : []),
      ]
    })
  return <div className="space-y-1">{render(tree, 0)}</div>
}

// ToolDetail shows one tool call's input arguments and its return content
// (matched files, command/diff output, or a result message).
export function ToolDetail({ tool }: { tool: ToolEvent }) {
  const matched = (tool.files ?? []).filter((f) => f.kind === 'matched')
  const out = tool.output ?? []
  const outScroll = useStickToBottom<HTMLPreElement>(out.length)
  const img = tool.image
  const imgSrc = img ? (img.url || (img.data ? `data:${img.media_type || 'image/png'};base64,${img.data}` : '')) : ''
  return (
    <div className="min-w-0 flex flex-col gap-2.5">
      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">参数</div>
        <ToolInputView tool={tool} />
      </div>

      <div>
        <div className="text-[11px] text-faint mb-1 tracking-wide">
          输出{imgSrc ? ' · 图片' : matched.length > 0 ? ` · ${matched.length}${tool.filesTotal && tool.filesTotal > matched.length ? `/${tool.filesTotal}` : ''} 个匹配` : out.length > 0 ? ` · ${out.length} 行` : ''}
        </div>
        {imgSrc ? (
        <div className="bg-surface2 border border-line rounded-[8px] p-2 inline-block max-w-full">
          <img src={imgSrc} alt="" className="max-h-[340px] max-w-full rounded-[5px] block" />
        </div>
      ) : matched.length > 0 ? (
        <div className="bg-surface2 border border-line rounded-[8px] py-2 px-2.5 max-h-[360px] overflow-auto">
          <MatchedFileTree paths={matched.slice(0, 400).map((f) => f.path)} />
        </div>
      ) : out.length > 0 ? (
        <pre ref={outScroll.ref} onScroll={outScroll.onScroll} className="m-0 font-mono text-[12px] leading-[1.55] bg-surface2 border border-line rounded-[8px] py-2 max-h-[360px] overflow-auto">
          {out.slice(0, 400).map((l, i) => (
            <div key={i} className={(l.stream || '').startsWith('diff') ? `cl ${l.stream}` : lineClass(l.stream)}>{l.text}</div>
          ))}
          {tool.outputTruncated && <div className="px-2.5 text-faint">… 输出已截断</div>}
        </pre>
      ) : (
        <div className="text-faint text-[12.5px] bg-surface2 border border-line rounded-[8px] py-2 px-2.5">（无返回内容）</div>
      )}
      </div>
    </div>
  )
}
