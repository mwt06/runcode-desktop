import { useEffect, useRef } from 'react'
import { toolTargetPath } from '@/chat/tool-text'
import { lastPreviewablePath, toWorkspaceRel } from '@/preview/classify'
import type { Block } from '@/chat/blocks'

// useAutoPreview 在一个回合结束时，自动打开这一回合新写出的最后一个可预览文件。
// 只看"本回合"产生的块：回合开始时记下当前长度，结束时从那里往后扫，所以历史里
// 早先写过的文件不会被反复弹出来。
export function useAutoPreview({ busy, blocks, cwd, enabled, open }: {
  busy: boolean
  blocks: Block[]
  cwd: string
  enabled: boolean
  open: (relPath: string) => void
}) {
  // 事件式逻辑靠 busy 的跳变驱动，所以其余入参走 ref 取最新值，避免把它们写进
  // 依赖数组后每次渲染都重跑。
  const latest = useRef({ blocks, cwd, enabled, open })
  latest.current = { blocks, cwd, enabled, open }
  const turnStartLen = useRef(0)
  const prevBusy = useRef(false)
  useEffect(() => {
    if (!prevBusy.current && busy) {
      // Turn starting: remember where this turn's blocks begin.
      turnStartLen.current = latest.current.blocks.length
    } else if (prevBusy.current && !busy) {
      // Turn ended: open the newest previewable file THIS turn wrote (if any).
      const { blocks: bs, cwd: dir, enabled: on, open: openFile } = latest.current
      if (on) {
        const paths: string[] = []
        for (const b of bs.slice(turnStartLen.current)) {
          if (b.kind !== 'tool') continue
          const t = b.tool
          if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
            const p = toolTargetPath(t)
            if (p) paths.push(toWorkspaceRel(p, dir))
          }
        }
        const rel = lastPreviewablePath(paths)
        if (rel) openFile(rel)
      }
    }
    prevBusy.current = busy
  }, [busy])
}
