import { useEffect, useRef, type RefObject } from 'react'
import { toolTargetPath } from '@/chat/tool-text'
import { extractFilePaths, rankAutoPreview, toWorkspaceRel } from '@/preview/classify'
import { resolveArtifactPath } from '@/core/bridge'
import type { Block } from '@/chat/blocks'

// 最多验证前几个候选就收手：助手正文里往往会提到一串路径，逐个问后端既慢又没必要
// ——真正的产物总在按价值排序的最前面。
const maxProbes = 6

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
//
// 候选来自两处，缺一不可：
//   · Write/Edit 工具写过的文件；
//   · 助手正文里提到的路径——**真正的产物经常不是 Write 写出来的**。做 PPT 时
//     .pptx 由 Bash 跑 python-pptx 生成，Write 只写了旁边的讲稿 .md，只看工具就
//     永远只剩 md 可选，于是"有 pptx 却打开 md"。
//
// 候选按价值排序后**逐个验证是否真的存在**，第一个在的才打开：Write 写过的文件可能
// 在同一回合内被改名或删除（notes/part2.md 就是这么没的），正文里提到的更可能只是
// 模型的说法。不能改用工作区文件清单来筛——那份清单在回合结束时才异步刷新，此刻
// 还是上一轮的旧值，本回合刚生成的 .pptx 恰恰不在里面。
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
          if (b.kind === 'tool') {
            const t = b.tool
            if ((t.toolName === 'Write' || t.toolName === 'Edit') && t.type === 'completed') {
              const p = toolTargetPath(t)
              if (p) paths.push(toWorkspaceRel(p, dir))
            }
          } else if (b.kind === 'assistant') {
            // 正文里的路径可能是绝对的（"已生成 D:\演示\…\deck.pptx"），换算成
            // 工作区相对路径后才与其它候选同形。
            for (const token of extractFilePaths(b.text)) paths.push(toWorkspaceRel(token, dir))
          }
        }
        void openFirstExisting(rankAutoPreview(paths), openFile)
      }
    }
    prevBusy.current = busy
  }, [busy])
}

// openFirstExisting 按价值顺序验证候选，打开第一个真实存在的。
//
// 存在性问后端而不是查前端的工作区清单：清单在回合结束时才异步刷新，此刻仍是上一轮
// 的旧值，本回合刚生成的产物必然缺席。resolveArtifactPath 对不存在的路径会失败（后端
// 归一化成 not_found），正好当作探针——顺带把越界路径也挡掉。
async function openFirstExisting(ranked: string[], openFile: (relPath: string) => void) {
  for (const rel of ranked.slice(0, maxProbes)) {
    try {
      await resolveArtifactPath(rel)
    } catch {
      continue // 不在了（或不可达）：换下一个，别开一个注定失败的标签页
    }
    openFile(rel)
    return
  }
}
