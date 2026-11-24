// 栅格生图查看容器（只读大图）。
// 当前只挂载只读大图；查看器的标注模式与脏状态由 context 保留，
// image-edit-* 也完整保留，重新启用入口时无需重建能力。
import { BTN } from '@/ui/tokens'
import { GeneratedImageModalSurface } from './generated-image-modal-surface'
import { useGeneratedImageViewer } from './generated-image-viewer-context'

export function GeneratedImageViewerHost() {
  const v = useGeneratedImageViewer()
  if (!v.active || !v.target) return null
  const { target, artifact } = v
  const url = artifact?.url ?? ''

  return (
    <GeneratedImageModalSurface
      open={v.active}
      suspended={v.suspended}
      confirmOpen={false}
      onRequestClose={v.requestClose}
    >
      <div className="flex items-center gap-2 px-4 py-3 border-b border-line2">
        <span className="text-[14px] font-medium text-ink flex-none">查看图片</span>
        <div className="flex-1" />
        <button type="button" className={`${BTN} !px-3`} onClick={v.requestClose}>关闭</button>
      </div>
      <div className="flex-1 min-h-0 overflow-hidden bg-surface2">
        <div className="h-full flex items-center justify-center p-4">
          {url ? (
            <img src={url} alt={target.alt} className="max-w-full max-h-[80vh] object-contain rounded-lg" />
          ) : (
            <p className="text-[12px] text-muted">加载中…</p>
          )}
        </div>
      </div>
    </GeneratedImageModalSurface>
  )
}
