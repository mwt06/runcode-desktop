import { useEffect, useRef, type RefObject } from 'react'
import { toolTargetPath } from '@/chat/tool-text'
import { pickAutoPreview, toWorkspaceRel } from '@/preview/classify'
import type { Block } from '@/chat/blocks'

// useAutoPreview 在一个回合结束时，自动打开这一回合写出的**最值得看的**那个文件。
// 只看"本回合"产生的块：回合开始时记下当前长度，结束时从那里往后扫，所以历史里
// 早先写过的文件不会被反复弹出来。
//
// 两条规则，都是为了"不要替用户做主"：
//   1. 本回合已经打开过预览（模型调 open_preview、用户点了产物卡片或文件浏览器）
//      就不再自动开——那面板上放着的是有人特意放上去的东西，回合末尾把它顶掉是
//      最讨嫌的一种"贴心"。
//   2. 挑哪个文件按产物价值排（文档 > h5 > md > 代码），不是按谁写得晚：做一套
//      PPT 的回合往往先写 .pptx 再写脚本或说明，按时间取就总在开副产物。
export function useAutoPreview({ busy, blocks, cwd, enabled, opens, open }: {
  busy: boolean
  blocks: Block[]
  cwd: string
  enabled: boolean
  // opens 是预览面板的累计打开次数（任何来源）。回合首尾各采样一次，差值 > 0 就
  // 说明这一轮已经有人开过预览了。
  opens: RefObject<number>
  open: (relPath: string) => void
}) {
  // 事件式逻辑靠 busy 的跳变驱动，所以其余入参走 ref 取最新值，避免把它们写进
  // 依赖数组后每次渲染都重跑。
  const latest = useRef({ blocks, cwd, enabled, opens, open })
  latest.current = { blocks, cwd, enabled, opens, open }
  const turnStartLen = useRef(0)
  const opensAtTurnStart = useRef(0)
  const prevBusy = useRef(false)
  useEffect(() => {
    if (!prevBusy.current && busy) {
      // Turn starting: remember where this turn's blocks begin, and how many
      // previews had been opened before it.
      turnStartLen.current = latest.current.blocks.length
      opensAtTurnStart.current = latest.current.opens.current ?? 0
    } else if (prevBusy.current && !busy) {
      // Turn ended: open this turn's most valuable written file — unless a preview
      // was already opened during it, in which case the panel is showing something
      // deliberate and must be left alone.
      const { blocks: bs, cwd: dir, enabled: on, opens, open: openFile } = latest.current
      const alreadyShown = (opens.current ?? 0) > opensAtTurnStart.current
      if (on && !alreadyShown) {
        const paths: string[] = []
        for (const b of bs.slice(turnStartLen.current)) {
          if (b.kind !== 'tool') continue
          const t = b.tool
          if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
            const p = toolTargetPath(t)
            if (p) paths.push(toWorkspaceRel(p, dir))
          }
        }
        const rel = pickAutoPreview(paths)
        if (rel) openFile(rel)
      }
    }
    prevBusy.current = busy
  }, [busy])
}
