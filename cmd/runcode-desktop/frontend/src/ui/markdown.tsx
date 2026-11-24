import { useMemo, useRef, type ReactNode } from 'react'
import ReactMarkdown, { type ExtraProps } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'

// CodeBlockRenderInfo 把围栏代码块的原始信息交给可选插槽。value 来自 hast 文本节点
// 拼接（rehype-highlight 之后仍保留原文），不能用 String(React children)——高亮后
// children 是带样式的元素树。start/end 是原文偏移，供围栏完整性判断使用。
export type CodeBlockRenderInfo = {
  lang: string
  meta: string
  value: string
  start: number
  end: number
  defaultNode: ReactNode
}

export type MarkdownProps = {
  children: string
  onOpenFile?: (relPath: string) => void
  resolveFile?: (token: string) => string | null
  textOnly?: boolean
  // 未传或返回 null 时保持原代码块；只影响本实例，不改变其它 Markdown 调用点。
  renderCodeBlock?: (info: CodeBlockRenderInfo) => ReactNode | null
}

function hastText(node: unknown): string {
  if (!node || typeof node !== 'object') return ''
  const n = node as { type?: string; value?: string; children?: unknown[] }
  if (n.type === 'text') return n.value ?? ''
  if (Array.isArray(n.children)) return n.children.map(hastText).join('')
  return ''
}

function classList(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String)
  if (typeof value === 'string') return value.split(/\s+/).filter(Boolean)
  return []
}

/** 只直接渲染 WebView 能自行加载的图片源；相对文件路径由专用预览链路展示。 */
function canRenderImageSource(src: unknown): boolean {
  return /^(?:https?:|data:image\/|blob:|file:)/i.test(String(src ?? '').trim())
}

/**
 * Markdown 渲染器。
 *
 * ReactMarkdown 把 components 中的函数视为元素类型，函数身份变化会重挂载整棵子树。
 * 这里用一次性的稳定组件加 latest ref：父组件因输入框打字而重渲染时，只更新内容，
 * 不重建 p/li/strong/code/pre 等节点。
 */
export function Markdown({ children, onOpenFile, resolveFile, textOnly = false, renderCodeBlock }: MarkdownProps) {
  const renderState = useRef({ source: children, textOnly, onOpenFile, resolveFile, renderCodeBlock })
  renderState.current = { source: children, textOnly, onOpenFile, resolveFile, renderCodeBlock }

  const components = useMemo(() => {
    function PreBlock({ node, children: preChildren, ...props }: JSX.IntrinsicElements['pre'] & ExtraProps) {
      const current = renderState.current
      const defaultNode = <pre className="my-2 bg-inset border border-line2 rounded-lg p-3 overflow-auto text-[13px] font-mono leading-[1.55]" {...props}>{preChildren}</pre>
      if (current.textOnly || !current.renderCodeBlock || !node) return defaultNode
      const code = node.children?.[0]
      if (!code || code.type !== 'element' || code.tagName !== 'code') return defaultNode
      const start = node.position?.start?.offset
      const end = node.position?.end?.offset
      if (typeof start !== 'number' || typeof end !== 'number') return defaultNode
      const langClass = classList(code.properties?.className).find((c) => c.startsWith('language-'))
      const lang = langClass ? langClass.slice('language-'.length) : ''
      const meta = typeof (code.data as { meta?: unknown } | undefined)?.meta === 'string'
        ? (code.data as { meta: string }).meta
        : ''
      // highlight 后的文本末尾常带换行；classify 会 trim，这里保留解析器原文形状。
      const value = hastText(code).replace(/\n$/, '')
      // 用原文切片核对偏移仍落在本条消息里，防止错位时误判围栏。
      if (start < 0 || end > current.source.length || end <= start) return defaultNode
      return current.renderCodeBlock({ lang, meta, value, start, end, defaultNode }) ?? defaultNode
    }

    function P({ node: _node, ...props }: JSX.IntrinsicElements['p'] & ExtraProps) {
      return <p className="my-2 first:mt-0 last:mb-0 leading-[1.75]" {...props} />
    }
    function Ul({ node: _node, ...props }: JSX.IntrinsicElements['ul'] & ExtraProps) {
      return <ul className="my-2 pl-5 list-disc space-y-1" {...props} />
    }
    function Ol({ node: _node, ...props }: JSX.IntrinsicElements['ol'] & ExtraProps) {
      return <ol className="my-2 pl-5 list-decimal space-y-1" {...props} />
    }
    function Li({ node: _node, ...props }: JSX.IntrinsicElements['li'] & ExtraProps) {
      return <li className="leading-[1.7]" {...props} />
    }
    function Strong({ node: _node, ...props }: JSX.IntrinsicElements['strong'] & ExtraProps) {
      return <strong className="font-semibold text-ink" {...props} />
    }
    function A({ node: _node, href, children: aChildren, ...props }: JSX.IntrinsicElements['a'] & ExtraProps) {
      const current = renderState.current
      if (current.textOnly) return <span>{aChildren}</span>
      const rel = href && current.resolveFile ? current.resolveFile(href) : null
      if (rel && current.onOpenFile) {
        return (
          <a className="text-primaryink underline underline-offset-2 cursor-pointer" href={href} onClick={(e) => { e.preventDefault(); current.onOpenFile?.(rel) }}>
            {aChildren}
          </a>
        )
      }
      return <a className="text-primaryink underline underline-offset-2" target="_blank" rel="noreferrer" href={href} {...props}>{aChildren}</a>
    }
    function H1({ node: _node, ...props }: JSX.IntrinsicElements['h1'] & ExtraProps) {
      return <h1 className="text-[18px] font-bold mt-3 mb-1.5" {...props} />
    }
    function H2({ node: _node, ...props }: JSX.IntrinsicElements['h2'] & ExtraProps) {
      return <h2 className="text-[16px] font-bold mt-3 mb-1.5" {...props} />
    }
    function H3({ node: _node, ...props }: JSX.IntrinsicElements['h3'] & ExtraProps) {
      return <h3 className="text-[15px] font-semibold mt-2.5 mb-1" {...props} />
    }
    function Blockquote({ node: _node, ...props }: JSX.IntrinsicElements['blockquote'] & ExtraProps) {
      return <blockquote className="border-l-2 border-line2 pl-3 my-2 text-muted" {...props} />
    }
    function Table({ node: _node, ...props }: JSX.IntrinsicElements['table'] & ExtraProps) {
      return <table className="my-2 border-collapse text-[13px] block overflow-auto" {...props} />
    }
    function Th({ node: _node, ...props }: JSX.IntrinsicElements['th'] & ExtraProps) {
      return <th className="border border-line2 px-2 py-1 bg-surface2 text-left" {...props} />
    }
    function Td({ node: _node, ...props }: JSX.IntrinsicElements['td'] & ExtraProps) {
      return <td className="border border-line2 px-2 py-1" {...props} />
    }
    function Img({ node: _node, src, alt, className, ...props }: JSX.IntrinsicElements['img'] & ExtraProps) {
      const current = renderState.current
      if (current.textOnly) return <span>{alt || '图片'}</span>
      // 相对本地路径由文件预览链路处理；直接挂 img 会先显示破图占位。
      if (!canRenderImageSource(src)) return null
      return <img src={src} alt={alt ?? ''} className={`my-2 max-w-full rounded-lg ${className ?? ''}`.trim()} {...props} />
    }
    function Code({ node: _node, className, children: codeChildren, ...rest }: JSX.IntrinsicElements['code'] & ExtraProps) {
      const current = renderState.current
      const text = String(codeChildren ?? '')
      const isBlock = /language-/.test(className || '') || text.includes('\n')
      if (isBlock) return <code className={className} {...rest}>{codeChildren}</code>
      const rel = !current.textOnly && current.resolveFile ? current.resolveFile(text) : null
      if (rel && current.onOpenFile) {
        return (
          <code
            className="bg-inset text-primaryink px-1.5 py-0.5 rounded text-[0.9em] font-mono break-words cursor-pointer hover:underline"
            title={'预览 ' + rel}
            onClick={() => current.onOpenFile?.(rel)}
          >
            {codeChildren}
          </code>
        )
      }
      return (
        <code className="bg-inset text-[#7c3aed] px-1.5 py-0.5 rounded text-[0.9em] font-mono break-words" {...rest}>
          {codeChildren}
        </code>
      )
    }

    return {
      img: Img,
      p: P,
      ul: Ul,
      ol: Ol,
      li: Li,
      strong: Strong,
      a: A,
      h1: H1,
      h2: H2,
      h3: H3,
      blockquote: Blockquote,
      hr: () => <hr className="my-3 border-line" />,
      table: Table,
      th: Th,
      td: Td,
      pre: PreBlock,
      code: Code,
    }
  }, [])

  return (
    <div className="mdx">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[[rehypeHighlight, { detect: false, ignoreMissing: true }]]}
        components={components}
      >
        {children}
      </ReactMarkdown>
    </div>
  )
}
