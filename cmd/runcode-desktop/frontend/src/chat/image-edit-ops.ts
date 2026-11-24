// 画板操作栈：坐标一律 0–1 归一化，窗口缩放不改变结果。纯函数，供组件与单测共用。

export type NormPoint = { x: number; y: number }

export type CanvasOperation =
  | { kind: 'stroke'; color: string; width: number; points: NormPoint[] }
  | { kind: 'text'; color: string; text: string; x: number; y: number; fontSize: number }

export type ImageEditState = {
  operations: CanvasOperation[]
  redo: CanvasOperation[]
  selectedColor: string
  tool: 'pen' | 'text'
}

/** 6 个高对比预设色（红/橙/黄/绿/蓝/品红）。 */
export const IMAGE_EDIT_COLORS = ['#E03131', '#F08C00', '#F59F00', '#2F9E44', '#1971C2', '#C2255C'] as const

// protogen 不导出整型常量；与 protocol.ImageEditMaxEdge / ImageEditMaxBytes 保持同值。
export const IMAGE_EDIT_MAX_EDGE = 4096
export const IMAGE_EDIT_MAX_BYTES = 16 * 1024 * 1024

export function initialEditState(color: string = IMAGE_EDIT_COLORS[0]): ImageEditState {
  return { operations: [], redo: [], selectedColor: color, tool: 'pen' }
}

export function isDirty(state: ImageEditState): boolean {
  return state.operations.length > 0
}

export function selectColor(state: ImageEditState, color: string): ImageEditState {
  return { ...state, selectedColor: color }
}

export function selectTool(state: ImageEditState, tool: 'pen' | 'text'): ImageEditState {
  return { ...state, tool }
}

export function appendOperation(state: ImageEditState, op: CanvasOperation): ImageEditState {
  return { ...state, operations: [...state.operations, op], redo: [] }
}

export function appendStroke(
  state: ImageEditState,
  points: NormPoint[],
  width = 0.012,
): ImageEditState {
  if (points.length < 2) return state
  return appendOperation(state, { kind: 'stroke', color: state.selectedColor, width, points: points.map(clampPoint) })
}

export function appendText(
  state: ImageEditState,
  text: string,
  x: number,
  y: number,
  fontSize = 0.045,
): ImageEditState {
  const safe = sanitizePlainText(text)
  if (!safe) return state
  const p = clampPoint({ x, y })
  return appendOperation(state, { kind: 'text', color: state.selectedColor, text: safe, x: p.x, y: p.y, fontSize })
}

export function undo(state: ImageEditState): ImageEditState {
  if (state.operations.length === 0) return state
  const operations = state.operations.slice(0, -1)
  const last = state.operations[state.operations.length - 1]!
  return { ...state, operations, redo: [...state.redo, last] }
}

export function redo(state: ImageEditState): ImageEditState {
  if (state.redo.length === 0) return state
  const next = state.redo[state.redo.length - 1]!
  return { ...state, operations: [...state.operations, next], redo: state.redo.slice(0, -1) }
}

export function clampPoint(p: NormPoint): NormPoint {
  return { x: clamp01(p.x), y: clamp01(p.y) }
}

function clamp01(n: number): number {
  if (!Number.isFinite(n)) return 0
  return Math.min(1, Math.max(0, n))
}

/** 只保留纯文本：空白归一、去掉控制字符与 HTML 尖括号，避免注入。 */
export function sanitizePlainText(raw: string): string {
  return Array.from(raw ?? '')
    .map((ch) => {
      if (ch === '<' || ch === '>') return ''
      const c = ch.codePointAt(0) ?? 0
      if (c === 0x09 || c === 0x0a || c === 0x0d || ch === ' ') return ' '
      if (c < 0x20 || c === 0x7f) return ''
      return ch
    })
    .join('')
    .replace(/ {2,}/g, ' ')
    .trim()
    .slice(0, 200)
}

/** 合成前按最长边上限缩放画布尺寸。 */
export function fitEditOutputSize(width: number, height: number, maxEdge = IMAGE_EDIT_MAX_EDGE): { w: number; h: number } {
  const w = Math.max(1, Math.floor(width))
  const h = Math.max(1, Math.floor(height))
  const edge = Math.max(w, h)
  if (edge <= maxEdge) return { w, h }
  const scale = maxEdge / edge
  return { w: Math.max(1, Math.round(w * scale)), h: Math.max(1, Math.round(h * scale)) }
}

export const IMAGE_EDIT_FONT_FAMILY = '"IBM Plex Sans", "Microsoft YaHei", sans-serif'

export type PixelImageEditOperation =
  | { kind: 'stroke'; color: string; lineWidth: number; points: { x: number; y: number }[] }
  | { kind: 'text'; color: string; text: string; x: number; y: number; fontSize: number; fontFamily: string }

/** 覆盖层与 Canvas 共用：归一化操作投影到像素坐标。 */
export function projectImageEditOperations(
  operations: ImageEditState['operations'],
  width: number,
  height: number,
): PixelImageEditOperation[] {
  const W = Math.max(1, width)
  const H = Math.max(1, height)
  const minEdge = Math.min(W, H)
  return operations.map((op) => {
    if (op.kind === 'stroke') {
      return {
        kind: 'stroke',
        color: op.color,
        lineWidth: Math.max(1, op.width * minEdge),
        points: op.points.map((p) => ({ x: p.x * W, y: p.y * H })),
      }
    }
    return {
      kind: 'text',
      color: op.color,
      text: op.text,
      x: op.x * W,
      y: op.y * H,
      fontSize: Math.max(12, op.fontSize * minEdge),
      fontFamily: IMAGE_EDIT_FONT_FAMILY,
    }
  })
}
