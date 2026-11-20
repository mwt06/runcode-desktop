// 回合结束自动侧栏的候选收集：从本回合块里抽出路径，并排除「生成图片」产物。
// 生图不自动打开右侧预览；普通 Write/Edit 与正文提到的 pptx/md 等仍可进入。
import { toolTargetPath } from '@/chat/tool-text'
import type { Block } from '@/chat/blocks'
import { IMAGE_GENERATION_TOOL, isGeneratedImageRel } from '@/core/image-generation'
import { extractFilePaths, toWorkspaceRel } from '@/preview/classify'

function normRel(p: string): string {
  return p.replace(/\\/g, '/').replace(/^\.\//, '')
}

/** 正文里「生成图片/…」及绝对路径中夹带该目录的片段（extractFilePaths 吃不下中文目录名）。 */
function generatedPathsFromText(text: string, cwd: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(/生成图片[/\\][^\s"'<>|]+/g)) {
    out.push(normRel(toWorkspaceRel(m[0].replace(/[.,;:!?，。、）】]+$/, ''), cwd)))
  }
  for (const m of text.matchAll(/[A-Za-z]:[/\\][^\s"'<>|]*?生成图片[/\\][^\s"'<>|]+/g)) {
    out.push(normRel(toWorkspaceRel(m[0].replace(/[.,;:!?，。、）】]+$/, ''), cwd)))
  }
  return out.filter(Boolean)
}

function imageGenerateResultPaths(blocks: Block[], cwd: string): Set<string> {
  const out = new Set<string>()
  for (const b of blocks) {
    if (b.kind === 'assistant') {
      for (const p of generatedPathsFromText(b.text, cwd)) out.add(p)
      continue
    }
    if (b.kind !== 'tool' || b.tool.toolName !== IMAGE_GENERATION_TOOL) continue
    const data = b.tool.data as { images?: Array<{ path?: string }> } | undefined
    for (const img of data?.images ?? []) {
      const p = img?.path?.trim()
      if (!p) continue
      out.add(normRel(toWorkspaceRel(p, cwd)))
    }
  }
  return out
}

function shouldExclude(rel: string, generated: Set<string>): boolean {
  const n = normRel(rel)
  if (!n) return true
  if (isGeneratedImageRel(n)) return true
  if (generated.has(n)) return true
  for (const g of generated) {
    if (n === g || n.endsWith('/' + g) || g.endsWith('/' + n)) return true
  }
  return false
}

/**
 * collectAutoPreviewCandidates 收集本回合可自动侧栏预览的工作区相对路径。
 * 来源：完成态 Write/Edit 目标 + 助手正文里的路径；排除生成图片目录与本回合生图结果。
 */
export function collectAutoPreviewCandidates(blocks: Block[], cwd: string): string[] {
  const generated = imageGenerateResultPaths(blocks, cwd)
  const paths: string[] = []
  for (const b of blocks) {
    if (b.kind === 'tool') {
      const t = b.tool
      if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
        const p = toolTargetPath(t)
        if (p) paths.push(toWorkspaceRel(p, cwd))
      }
    } else if (b.kind === 'assistant') {
      for (const token of extractFilePaths(b.text)) paths.push(toWorkspaceRel(token, cwd))
    }
  }
  return paths.filter((p) => !shouldExclude(p, generated))
}
