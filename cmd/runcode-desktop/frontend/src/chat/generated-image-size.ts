import type { ToolEvent } from '@/core/bridge'

export type PreviewDisplaySize = { width: number; height: number; provisional: boolean }
export type PreviewSizeHint = { w: number; h: number }
export type PreviewGeometry = {
  key: string
  width: number
  height: number
  locked: boolean
}

/** 等比缩放：Wcap=min(560,C) Hcap=min(560,0.65×vh)，正方形可到对话内常见大图尺寸；不放大小图。 */
export function computePreviewDisplaySize(
  naturalW: number,
  naturalH: number,
  containerW: number,
  viewportH: number,
): PreviewDisplaySize {
  const w = Math.max(1, naturalW)
  const h = Math.max(1, naturalH)
  const C = Math.max(1, containerW)
  const Wcap = Math.min(560, C)
  const Hcap = Math.min(560, 0.65 * Math.max(1, viewportH))
  const s = Math.min(1, Wcap / w, Hcap / h)
  return { width: Math.max(1, Math.round(w * s)), height: Math.max(1, Math.round(h * s)), provisional: false }
}

/** 从工具输入或结果读取尺寸提示；生成早期可通过它提前预留接近真实的舞台比例。 */
export function parseSizeHint(tool: ToolEvent): PreviewSizeHint | null {
  const raw = (() => {
    const input = tool.input
    if (input && typeof input === 'object' && typeof (input as { size?: unknown }).size === 'string') {
      return (input as { size: string }).size
    }
    const data = tool.data
    if (data && typeof data === 'object' && typeof (data as { size?: unknown }).size === 'string') {
      return (data as { size: string }).size
    }
    return ''
  })()
  const m = /^(\d+)\s*[x×]\s*(\d+)$/i.exec((raw || '').trim())
  if (!m) return null
  const w = Number(m[1])
  const h = Number(m[2])
  if (!Number.isFinite(w) || !Number.isFinite(h) || w <= 0 || h <= 0) return null
  return { w, h }
}

/**
 * 生成期间允许提示尺寸补入一次；完成后锁住已验证的舞台几何，
 * 防止真实图片宽高变化推动整个对话栏。
 */
export function resolvePreviewGeometry(
  current: PreviewGeometry | null,
  next: {
    key: string
    width: number
    height: number
    running: boolean
    hint: PreviewSizeHint | null
  },
): PreviewGeometry {
  const width = Math.max(1, next.width)
  const height = Math.max(1, next.height)
  if (!current || current.key !== next.key) {
    return { key: next.key, width, height, locked: !next.running }
  }
  if (next.running && !current.locked && next.hint) {
    return { ...current, width: next.hint.w, height: next.hint.h }
  }
  if (!next.running && !current.locked) {
    return { ...current, locked: true }
  }
  return current
}
