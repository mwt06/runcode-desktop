// ReplyArtifacts renders the regex-matched workspace files mentioned in an assistant
// reply as clickable cards. Memoized so it only recomputes when the reply text or the
// workspace file list changes — not on every streaming re-render.
import { useMemo } from 'react'
import { CollapsibleGroup } from '@/ui/collapsible-group'
import { extractFilePaths, matchWorkspaceFiles } from '@/preview/classify'
import { type PreviewTab } from '@/preview/tabs'
import { ArtifactCard } from './artifact-card'

export function ReplyArtifacts({ text, files, tabs, onOpen }: { text: string; files: string[]; tabs: PreviewTab[]; onOpen: (relPath: string) => void }) {
  const paths = useMemo(() => matchWorkspaceFiles(extractFilePaths(text), files), [text, files])
  if (paths.length === 0) return null
  return (
    <CollapsibleGroup icon="eye" label="可预览文件" count={paths.length}>
      {paths.map((p) => (
        <ArtifactCard key={p} relPath={p} add={0} del={0} onOpen={onOpen} autoOpened={tabs.some((t) => t.kind === 'file' && t.relPath === p)} />
      ))}
    </CollapsibleGroup>
  )
}
