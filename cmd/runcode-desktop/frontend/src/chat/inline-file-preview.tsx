import { useEffect, useRef, useState } from 'react'
import { errCode, openArtifact, readArtifact, releaseArtifact } from '@/core/bridge'
import {
  codePreviewTarget,
  type CodePreviewTarget,
} from '@/preview/code-preview'
import { noteInlineFile } from '@/session/inline-preview-status'
import { InlinePreview } from './inline-preview'

type Props = {
  path: string
  sessionId?: string
  messageId?: string
  previewId?: string
  onOpenCodePreview?: (target: CodePreviewTarget) => void
}

function fileKind(path: string): 'svg' | 'mermaid' | 'html' | null {
  const ext = path.replace(/\\/g, '/').split('/').pop()?.split('.').pop()?.toLowerCase() ?? ''
  if (ext === 'svg') return 'svg'
  if (ext === 'mmd' || ext === 'mermaid') return 'mermaid'
  if (ext === 'html' || ext === 'htm') return 'html'
  return null
}

export function InlineFilePreview({ path, sessionId, messageId, previewId, onOpenCodePreview }: Props) {
  const kind = fileKind(path)
  const [source, setSource] = useState<string | null>(null)
  const [error, setError] = useState('')
  const [missing, setMissing] = useState(false)
  const openRef = useRef(onOpenCodePreview)
  openRef.current = onOpenCodePreview

  useEffect(() => {
    if (!sessionId || !kind) {
      setSource(null)
      setError(kind ? '' : '该文件类型不支持代码预览')
      setMissing(false)
      return
    }
    let cancelled = false
    let reference = ''
    let released = false
    const release = () => {
      if (!reference || released) return
      released = true
      void releaseArtifact(reference).catch(() => {})
    }
    noteInlineFile(sessionId, path, 'pending')
    setSource(null)
    setError('')
    setMissing(false)
    openArtifact(sessionId, path)
      .then((artifact) => {
        if (cancelled || artifact.sessionId !== sessionId) {
          void releaseArtifact(artifact.reference).catch(() => {})
          return null
        }
        reference = artifact.reference
        return readArtifact(reference)
      })
      .then((text) => {
        if (text == null || cancelled) return
        setSource(text)
        noteInlineFile(sessionId, path, 'rendered')
      })
      .catch((reason: unknown) => {
        if (cancelled) return
        setMissing(errCode(reason) === 'not_found')
        setError('暂时无法预览')
        noteInlineFile(sessionId, path, 'fallback')
        void reason
      })
      .finally(() => {
        release()
      })
    return () => {
      cancelled = true
      release()
    }
  }, [path, sessionId, kind])

  const openTarget = () => {
    if (!onOpenCodePreview) return
    onOpenCodePreview(codePreviewTarget({
      kind: kind ?? 'svg',
      source: source ?? '',
      title: path.replace(/\\/g, '/').split('/').pop() ?? path,
      sessionId,
      messageId,
      previewId: previewId || path,
      relatedPaths: kind && kind !== 'mermaid' ? [path] : [],
    }))
  }

  return (
    <section data-file-preview-path={path} className="my-2">
      {source && kind ? (
        <InlinePreview
          kind={kind}
          source={source}
          defaultNode={<pre className="m-0">{source}</pre>}
          messageId={messageId}
          previewId={previewId || path}
          sessionId={sessionId}
          relatedPaths={[path]}
          onOpenCodePreview={onOpenCodePreview}
        />
      ) : error ? (
        <div className="flex items-center gap-2 rounded-lg border border-line2 bg-surface px-3 py-2 text-[12px]">
          <span className="text-muted min-w-0 flex-1 truncate" title={path}>
            {missing ? '文件不存在' : error}：{path}
          </span>
          <button type="button" className="text-primary hover:underline flex-none" onClick={openTarget}>
            在右侧预览中打开
          </button>
        </div>
      ) : (
        <div className="min-h-12 rounded-lg border border-line2 bg-inset/40" aria-hidden />
      )}
    </section>
  )
}
