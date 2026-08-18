// 预览的加载态，五处共用：文本/代码/Markdown 的读取，Office 转 PDF，以及三个
// 内嵌渲染器（docx / pptx / xlsx）的懒加载。
//
// 此前这五处各写各的：三个渲染器和 Office 转换各摆一行静态的"正在加载…"，而文本
// 类干脆什么都不画——面板一片空白，看不出是在加载还是这文件本来就是空的。
import { useEffect, useState } from 'react'
import { Spinner } from '@/ui/glyphs'

// 出现前的静默期。文本预览通常几十毫秒就回来了，立刻画一个转圈只会闪一下，比空
// 白更烦人；超过这个时长才认为"用户确实在等"。低于约 200ms 的等待人感知为"立即"，
// 取 180ms 留一点余量。
const appearDelayMs = 180

// useDelayed 在 delay 之后才返回 true。卸载时清掉定时器，所以加载很快结束时这个
// 组件从头到尾都不会渲染出任何东西。
function useDelayed(delay: number): boolean {
  const [show, setShow] = useState(false)
  useEffect(() => {
    const timer = setTimeout(() => setShow(true), delay)
    return () => clearTimeout(timer)
  }, [delay])
  return show
}

// PreviewLoading 是慢操作的加载态：转圈 + 一句说明。
//
// 用 spinner 而不是骨架，是因为这些等待都长到需要**解释**——"正在用本机 Office
// 生成高保真预览"告诉用户为什么要等这几秒，以及等的是什么；骨架屏只能表示"在加载"。
export function PreviewLoading({ hint }: { hint: string }) {
  const show = useDelayed(appearDelayMs)
  if (!show) return null
  return (
    <div className="p-6 flex items-center gap-2.5 text-[13px] text-muted anim-rise">
      <Spinner size={14} />
      <span>{hint}</span>
    </div>
  )
}

// 骨架的行宽（百分比）。刻意不等长且不规则，像一段真实的文字：等宽的几条会读成
// 表格或进度条。首行短——文档通常以标题开头。
const skeletonRows = [38, 92, 86, 95, 72, 90, 88, 60]

// PreviewSkeleton 是文本类预览的加载态：几行扫光的占位条。
//
// 这里用骨架而不是转圈：等待很短且无需解释，而占位条能顺带告诉用户"要来的是一篇
// 文字"，面板也不会先塌成空白再被内容撑开。
export function PreviewSkeleton() {
  const show = useDelayed(appearDelayMs)
  if (!show) return null
  return (
    <div className="p-4 flex flex-col gap-2.5 anim-rise" aria-label="正在加载预览" aria-busy="true">
      {skeletonRows.map((width, i) => (
        <div key={i} className="skeleton h-[13px] rounded-md" style={{ width: `${width}%` }} />
      ))}
    </div>
  )
}
