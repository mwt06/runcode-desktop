// 栅格生图展示投影：按来源 session + 完整规范相对路径匹配 image_generate 与 open_preview。
// 不按 basename 去重；不修改原始 blocks；副本选中只来自 context.selectedCopies。
import type { ImageGenerationArtifact, ToolEvent } from '@/core/bridge'
import { IMAGE_GENERATION_TOOL, isImageGenerationResult, isRasterImageGeneration } from '@/core/image-generation'
import type { Block } from './blocks'

export type GeneratedImagePresentation = {
  ownerBlockId: string
  sessionId: string
  sessionEpoch: number
  /** 本条生图当前应展示的规范相对路径（含选中副本）。 */
  paths: string[]
  /** 已合并进本条预览标题的 open_preview 工具 use id。 */
  mergedPreviewToolIds: string[]
}

export type GeneratedImagePresentationContext = {
  sessionId: string
  sessionEpoch: number
  selectedCopies: ReadonlyMap<string, ImageGenerationArtifact>
}

/** 规范为工作区相对、正斜杠、无首尾 / 的路径；不解析 ..。 */
export function normalizePresentationPath(path: string, cwd = ''): string {
  let p = (path || '').trim().replace(/\\/g, '/')
  if (!p) return ''
  const root = (cwd || '').trim().replace(/\\/g, '/').replace(/\/+$/, '')
  if (root) {
    const lowerP = p.toLowerCase()
    const lowerRoot = root.toLowerCase()
    if (lowerP === lowerRoot) return ''
    if (lowerP.startsWith(lowerRoot + '/')) p = p.slice(root.length + 1)
  }
  p = p.replace(/^\/+/, '').replace(/\/+$/, '')
  return p
}

function toolPath(tool: ToolEvent): string {
  const fromFiles = tool.files?.[0]?.path
  if (typeof fromFiles === 'string' && fromFiles.trim()) return fromFiles
  const data = tool.data
  if (data && typeof data === 'object') {
    const o = data as { path?: unknown }
    if (typeof o.path === 'string') return o.path
  }
  const input = tool.input
  if (input && typeof input === 'object') {
    const o = input as { path?: unknown }
    if (typeof o.path === 'string') return o.path
  }
  return ''
}

function imagePaths(tool: ToolEvent, copy: ImageGenerationArtifact | undefined, cwd: string): string[] {
  const out: string[] = []
  if (copy?.path) {
    const n = normalizePresentationPath(copy.path, cwd)
    if (n) out.push(n)
  }
  const result = isImageGenerationResult(tool.data) ? tool.data : null
  for (const img of result?.images ?? []) {
    const n = normalizePresentationPath(img.path, cwd)
    if (n && !out.includes(n)) out.push(n)
  }
  return out
}

/**
 * buildGeneratedImagePresentation：从当前会话 blocks 投影栅格生图展示条目。
 * 同 session + 完整路径才合并 open_preview；跨 session／不同 generation 目录不合并。
 */
export function buildGeneratedImagePresentation(
  blocks: Block[],
  cwd: string,
  context: GeneratedImagePresentationContext,
): GeneratedImagePresentation[] {
  const presentations: GeneratedImagePresentation[] = []
  const pathOwners = new Map<string, string>() // path → ownerBlockId

  for (const b of blocks) {
    if (b.kind !== 'tool') continue
    if ((b.sessionId ?? '') !== context.sessionId) continue
    if (b.tool.toolName !== IMAGE_GENERATION_TOOL) continue
    const result = isImageGenerationResult(b.tool.data) ? b.tool.data : null
    if (!isRasterImageGeneration(b.tool, result)) continue
    const copy = context.selectedCopies.get(b.id)
    const paths = imagePaths(b.tool, copy, cwd)
    presentations.push({
      ownerBlockId: b.id,
      sessionId: context.sessionId,
      sessionEpoch: context.sessionEpoch,
      paths,
      mergedPreviewToolIds: [],
    })
    for (const p of paths) pathOwners.set(p, b.id)
  }

  const byId = new Map(presentations.map((p) => [p.ownerBlockId, p]))
  for (const b of blocks) {
    if (b.kind !== 'tool') continue
    if ((b.sessionId ?? '') !== context.sessionId) continue
    if (b.tool.toolName !== 'open_preview') continue
    if (b.tool.parentToolUseID) continue
    const path = normalizePresentationPath(toolPath(b.tool), cwd)
    if (!path) continue
    const ownerId = pathOwners.get(path)
    if (!ownerId) continue
    const owner = byId.get(ownerId)
    if (!owner) continue
    const tid = b.tool.toolUseID
    if (tid && !owner.mergedPreviewToolIds.includes(tid)) {
      owner.mergedPreviewToolIds.push(tid)
    }
  }

  return presentations
}

/** 执行卡过滤：指向本会话已匹配栅格生图产物的 open_preview 不单独占行。 */
export function isMergedRasterOpenPreview(
  tool: ToolEvent,
  presentations: GeneratedImagePresentation[],
  cwd: string,
  sessionId: string,
): boolean {
  if (tool.toolName !== 'open_preview') return false
  if (tool.parentToolUseID) return false
  const path = normalizePresentationPath(toolPath(tool), cwd)
  if (!path) return false
  for (const p of presentations) {
    if (p.sessionId !== sessionId) continue
    if (p.paths.includes(path)) return true
    if (tool.toolUseID && p.mergedPreviewToolIds.includes(tool.toolUseID)) return true
  }
  return false
}
