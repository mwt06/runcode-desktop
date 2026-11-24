// Mermaid 静态预览：源码预检 → 串行队列 → 本地 Mermaid 转 SVG → 与 SVG 相同的清理。
//
// Mermaid 模块由调用方注入加载函数（业务层按需动态导入、随客户端打包，不走 CDN），
// 本模块不直接依赖它，预检与队列因此可以独立测试。
//
// 安全配置由客户端固定：消息里的 %%{init}%% 指令或 frontmatter 一律退回源码，不让
// 内容改写安全级别、主题或尺寸；click/callback/link 这类交互绑定同样退回。生成的 SVG
// 仍要过 sanitizeSvg，不单靠 securityLevel: 'strict'。
//
// 注意：普通超时打断不了主线程上同步执行的布局。防卡顿靠输入体积、语句/节点/连线
// 数量与串行队列，是否够用要在目标机上实测，不能写成"必然不会卡住"。
import { PREVIEW_LIMITS, utf8Bytes } from './static-detect'
import { sanitizeSvg, type SvgResult } from './static-svg'

// 白名单覆盖对话里常见图种（含 beta 命名）。未知头仍拒绝，避免把任意文本当图解析。
export const MERMAID_DIAGRAMS = new Set([
  'flowchart', 'graph',
  'sequenceDiagram',
  'classDiagram', 'classDiagram-v2',
  'stateDiagram', 'stateDiagram-v2',
  'erDiagram',
  'pie',
  'gantt',
  'gitGraph',
  'journey',
  'timeline',
  'mindmap',
  'quadrantChart',
  'requirementDiagram',
  'xychart', 'xychart-beta',
  'sankey', 'sankey-beta',
  'block', 'block-beta',
  'architecture', 'architecture-beta',
])

const FONT = '"Microsoft YaHei","PingFang SC","Noto Sans CJK SC","Source Han Sans SC",sans-serif'

export const MERMAID_CONFIG = {
  startOnLoad: false,
  securityLevel: 'strict',
  theme: 'default',
  fontFamily: FONT,
  htmlLabels: false,
  flowchart: { htmlLabels: false },
  maxTextSize: PREVIEW_LIMITS.mermaidBytes,
  maxEdges: PREVIEW_LIMITS.mermaidEdges,
  suppressErrorRendering: true,
  // secure 中的键不允许被图内指令覆盖；预检已拒绝指令，这里是第二道。
  secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'maxEdges', 'htmlLabels', 'flowchart', 'theme', 'themeVariables', 'themeCSS', 'fontFamily', 'dompurifyConfig'],
} as const

export type MermaidCheckFailure = 'empty' | 'too-large' | 'frontmatter' | 'directive' | 'interaction' | 'unsupported-type' | 'too-complex'
export type MermaidCheck = { ok: true; diagram: string } | { ok: false; reason: MermaidCheckFailure }

// 连线记号：-->、-->>、==>、-.->、->>、..>、~~~ 以及 ER 的 ||--o{ 里的 --。宁多数不少。
const EDGE = /-{2,}>*|={2,}>*|-\.+->?|->+|\.\.+|~{3,}/g
const FLOW_KEYWORDS = new Set(['subgraph', 'end', 'style', 'classDef', 'class', 'linkStyle', 'direction', 'accTitle', 'accDescr'])
// 交互绑定。负向先行断言排除以这些词作节点名的连线（`link --> B`）。
const INTERACTION = /^\s*(?:click|callback|links?)\s+(?![-=.~ox<>&|])\S/i

export function checkMermaidSource(source: string): MermaidCheck {
  const text = source.replace(/\r\n?/g, '\n')
  if (!text.trim()) return { ok: false, reason: 'empty' }
  if (utf8Bytes(text) > PREVIEW_LIMITS.mermaidBytes) return { ok: false, reason: 'too-large' }
  if (/%%\s*\{/.test(text)) return { ok: false, reason: 'directive' }
  const lines = text.split('\n').filter((l) => l.trim() && !/^\s*%%/.test(l))
  if (lines.length === 0) return { ok: false, reason: 'empty' }
  if (/^\s*---\s*$/.test(lines[0])) return { ok: false, reason: 'frontmatter' }
  const diagram = /^\s*(\S+)/.exec(lines[0])![1]
  if (!MERMAID_DIAGRAMS.has(diagram)) return { ok: false, reason: 'unsupported-type' }
  const statements = lines.slice(1).flatMap((l) => l.split(';')).filter((s) => s.trim())
  if (statements.some((s) => INTERACTION.test(s))) return { ok: false, reason: 'interaction' }
  if (statements.length > PREVIEW_LIMITS.mermaidStatements) return { ok: false, reason: 'too-complex' }
  let edges = 0
  for (const s of statements) edges += (s.match(EDGE) ?? []).length
  if (edges > PREVIEW_LIMITS.mermaidEdges) return { ok: false, reason: 'too-complex' }
  if ((diagram === 'flowchart' || diagram === 'graph') && countFlowNodes(statements) > PREVIEW_LIMITS.mermaidNodes) {
    return { ok: false, reason: 'too-complex' }
  }
  return { ok: true, diagram }
}

// 粗估流程图节点数：按连线切段，去掉 |标签|，按 & 拆分，取每段开头的标识符。
export function countFlowNodes(statements: string[]): number {
  const ids = new Set<string>()
  for (const s of statements) {
    for (let seg of s.split(EDGE)) {
      seg = seg.replace(/^\s*\|[^|]*\|/, '')
      for (const part of seg.split('&')) {
        const id = /^\s*([^\s[\](){}>|:;"'&]+)/.exec(part)?.[1]
        if (id && !FLOW_KEYWORDS.has(id)) ids.add(id)
      }
    }
  }
  return ids.size
}

// Mermaid 实例上本模块用到的最小接口。
export type MermaidApi = {
  initialize(config: Record<string, unknown>): void
  parse(text: string, opts?: { suppressErrors?: boolean }): Promise<unknown>
  render(id: string, text: string, container?: Element): Promise<{ svg: string }>
}

export type MermaidFailure = MermaidCheckFailure | 'busy' | 'timeout' | 'load-failed' | 'syntax' | 'cancelled' | 'unsafe-output' | 'platform-unsupported'
export type MermaidResult = Extract<SvgResult, { ok: true }> | { ok: false; reason: MermaidFailure }

// Mermaid 12 依赖 Constructable Stylesheets（new CSSStyleSheet）。麒麟旧 WebKit
// 没有该 API 时，提前失败并给出明确原因，避免 render 抛错后被静默吞掉。
export function mermaidRuntimeSupported(): boolean {
  try {
    if (typeof CSSStyleSheet === 'undefined') return false
    // 探测构造能力；部分环境有名无实。
    const sheet = new CSSStyleSheet()
    return !!sheet
  } catch {
    return false
  }
}

// createSerialQueue：Mermaid 的渲染共享模块级状态，并发调用会串扰，所以一次只跑
// 一个；排队的任务数有上限，超出直接拒绝而不是无限堆积。
export function createSerialQueue(maxPending = 12) {
  let tail: Promise<unknown> = Promise.resolve()
  let pending = 0
  return function enqueue<T>(task: () => Promise<T>): Promise<T> | null {
    if (pending >= maxPending) return null
    pending++
    const run = tail.then(task, task).finally(() => { pending-- })
    tail = run.catch(() => undefined)
    return run
  }
}

export type MermaidRenderer = (source: string, signal?: AbortSignal) => Promise<MermaidResult>

// createMermaidRenderer 把加载、初始化、排队、离屏测量容器与清理串成一个函数。
// 加载失败不缓存，下一次渲染会重试导入。
export function createMermaidRenderer(
  load: () => Promise<MermaidApi>,
  opts: { maxPending?: number; timeoutMs?: number } = {},
): MermaidRenderer {
  const enqueue = createSerialQueue(opts.maxPending)
  let api: Promise<MermaidApi> | null = null
  let seq = 0
  const ready = () => {
    if (!api) {
      const p = load().then((m) => { m.initialize({ ...MERMAID_CONFIG }); return m })
      api = p
      p.catch(() => { if (api === p) api = null })
    }
    return api
  }
  return async (source, signal) => {
    const check = checkMermaidSource(source)
    if (!check.ok) return check
    if (!mermaidRuntimeSupported()) return { ok: false, reason: 'platform-unsupported' }
    const run = enqueue(async (): Promise<MermaidResult> => {
      if (signal?.aborted) return { ok: false, reason: 'cancelled' }
      let mermaid: MermaidApi
      try {
        mermaid = await ready()
      } catch {
        return { ok: false, reason: 'load-failed' }
      }
      if (signal?.aborted) return { ok: false, reason: 'cancelled' }
      const id = `runcode-mmd-${++seq}`
      // 测量容器放在视口外而不是 display:none——后者会让文字测量得到 0，布局全错。
      const box = document.createElement('div')
      box.setAttribute('aria-hidden', 'true')
      box.style.cssText = 'position:fixed;left:-10000px;top:0;width:1024px;visibility:hidden;pointer-events:none;overflow:hidden'
      document.body.appendChild(box)
      let svg: string
      try {
        await mermaid.parse(source)
        svg = (await mermaid.render(id, source, box)).svg
      } catch (err) {
        // 运行时若仍撞上 CSSStyleSheet 等平台缺口，归为 platform-unsupported。
        const msg = err instanceof Error ? err.message : String(err)
        if (/CSSStyleSheet/i.test(msg)) return { ok: false, reason: 'platform-unsupported' }
        return { ok: false, reason: 'syntax' }
      } finally {
        box.remove()
        document.getElementById(id)?.remove()
        document.getElementById('d' + id)?.remove()
      }
      if (signal?.aborted) return { ok: false, reason: 'cancelled' }
      const clean = sanitizeSvg(svg)
      return clean.ok ? clean : { ok: false, reason: 'unsafe-output' }
    })
    if (!run) return { ok: false, reason: 'busy' }
    // 超时先让界面稳定回退源码；同步布局无法被定时器打断，任务结束后仍会自行清理 DOM。
    let timer = 0 as ReturnType<typeof setTimeout>
    const timeout = new Promise<MermaidResult>((resolve) => {
      timer = setTimeout(() => resolve({ ok: false, reason: 'timeout' }), opts.timeoutMs ?? 15_000)
    })
    try {
      return await Promise.race([run, timeout])
    } finally {
      clearTimeout(timer)
    }
  }
}
