// 邮件正文安全预览：复用 Markdown，但屏蔽图片与文件打开副作用。
import { Markdown } from '@/ui/markdown'

export function MailSafePreview({ markdown }: { markdown: string }) {
  // 去掉图片语法，避免加载远程图；不传 onOpenFile/resolveFile。
  const safe = markdown.replace(/!\[[^\]]*]\([^)]*\)/g, '[图片已省略]')
  return (
    <div className="text-[14px] text-ink leading-relaxed break-words [overflow-wrap:anywhere] max-w-full overflow-x-hidden">
      <Markdown textOnly={false}>{safe}</Markdown>
    </div>
  )
}
