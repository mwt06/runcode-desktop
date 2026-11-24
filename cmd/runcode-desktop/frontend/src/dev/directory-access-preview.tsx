// Isolated browser harness. The smoke driver supplies synthetic Wails bindings;
// no real account, workspace, native file picker, or Office process is used.
import { useState } from 'react'
import { DirectoryAccessSection } from '@/pages/directory-access'
import { useDirectoryAccess } from '@/session/use-directory-access'
import { usePreviewPanel } from '@/session/use-preview-panel'
import { BTN } from '@/ui/tokens'

export function DirectoryAccessPreview() {
  const [owner, setOwner] = useState('fixture-A')
  const directories = useDirectoryAccess(owner)
  const preview = usePreviewPanel(owner)
  return (
    <main className="min-h-screen bg-bg p-6">
      <div className="max-w-[920px] mx-auto flex flex-col gap-4">
        <h2 className="text-[20px] font-semibold text-ink m-0">目录授权交互测试</h2>
        <p className="text-[13px] text-muted m-0">仅使用本机合成绑定，不代表原生 Wails / Office 验收。</p>
        <div className="flex gap-2">
          <button className={BTN} onClick={() => setOwner('fixture-A')}>切到 A</button>
          <button className={BTN} onClick={() => setOwner('fixture-B')}>切到 B</button>
          <button className={BTN} onClick={() => void preview.openFile('报告.md')}>打开合成文件</button>
        </div>
        <output data-testid="source-owner">{owner}</output>
        <DirectoryAccessSection sessionID={owner} state={directories} />
        <output data-testid="preview-tabs">{preview.tabs.map((t) =>
          t.kind === 'file' ? `${t.artifact?.sessionId}:${t.artifact?.reference}`
            : t.kind === 'diff' ? t.relPath
              : t.ownerKey,
        ).join(',')}</output>
      </div>
    </main>
  )
}
