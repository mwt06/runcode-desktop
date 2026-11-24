// 标注导出：经 readArtifactBytes 取本源字节 → 本页 Blob → Canvas 合成 → PNG base64。
// 调用方持有 reference，负责保存期间不提前 release。
import { readArtifactBytes } from '@/core/bridge'
import {
  IMAGE_EDIT_MAX_BYTES, fitEditOutputSize, type ImageEditState,
  projectImageEditOperations,
} from './image-edit-ops'

export type ComposedImageEdit = {
  pngBase64: string
  width: number
  height: number
  /** PNG 实际二进制字节数。 */
  bytes: number
}

export async function composeImageEditPng(
  reference: string,
  state: ImageEditState,
): Promise<ComposedImageEdit> {
  const buf = await readArtifactBytes(reference)
  const blob = new Blob([buf], { type: 'image/png' })
  const blobUrl = URL.createObjectURL(blob)
  try {
    const img = await loadImage(blobUrl)
    const { w, h } = fitEditOutputSize(img.naturalWidth || 1, img.naturalHeight || 1)
    const canvas = document.createElement('canvas')
    canvas.width = w
    canvas.height = h
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error('无法创建画布')
    if (typeof document !== 'undefined' && document.fonts?.ready) {
      await document.fonts.ready.catch(() => {})
    }
    ctx.drawImage(img, 0, 0, w, h)
    const ops = projectImageEditOperations(state.operations, w, h)
    for (const op of ops) {
      if (op.kind === 'stroke') {
        if (op.points.length < 2) continue
        ctx.strokeStyle = op.color
        ctx.lineWidth = op.lineWidth
        ctx.lineCap = 'round'
        ctx.lineJoin = 'round'
        ctx.beginPath()
        op.points.forEach((p, i) => {
          if (i === 0) ctx.moveTo(p.x, p.y)
          else ctx.lineTo(p.x, p.y)
        })
        ctx.stroke()
      } else {
        ctx.fillStyle = op.color
        ctx.font = `${op.fontSize}px ${op.fontFamily}`
        ctx.textBaseline = 'alphabetic'
        ctx.fillText(op.text, op.x, op.y)
      }
    }
    const dataUrl = canvas.toDataURL('image/png')
    const i = dataUrl.indexOf(',')
    const pngBase64 = i >= 0 ? dataUrl.slice(i + 1) : dataUrl
    const bin = atob(pngBase64)
    const bytes = bin.length
    if (bytes > IMAGE_EDIT_MAX_BYTES) {
      throw new Error('合成结果超过 16 MiB 上限，请减少标注或使用更小原图')
    }
    return { pngBase64, width: w, height: h, bytes }
  } finally {
    URL.revokeObjectURL(blobUrl)
  }
}

function loadImage(url: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('无法加载底图'))
    img.src = url
  })
}
