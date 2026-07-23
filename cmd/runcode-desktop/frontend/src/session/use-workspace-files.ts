import { useCallback, useMemo, useState } from 'react'
import { listFiles } from '@/core/bridge'

export type WorkspaceFiles = ReturnType<typeof useWorkspaceFiles>

// 工作区文件清单：# 引用选择器、文件浏览器与回复产物匹配共用一份。
// reload 与 refresh 的差别是刻意的：切会话时列表必须换成新工作区的（失败就清空，
// 免得把上一个工作区的文件留在界面上），而回合结束的增量刷新失败时保留旧值，
// 一次抖动不该让整列表消失。
export function useWorkspaceFiles() {
  const [files, setFiles] = useState<string[]>([])

  const reload = useCallback(() => {
    listFiles()
      .then((f) => setFiles(f ?? []))
      .catch(() => setFiles([]))
  }, [])

  const refresh = useCallback(() => {
    listFiles()
      .then((f) => setFiles(f ?? []))
      .catch(() => {})
  }, [])

  // resolve maps a token written by the model (a bare name or a partial path) onto
  // an actual workspace file, so a reply's file mention can become a link.
  const resolve = useMemo(() => {
    const norm = (s: string) => s.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
    const set = files.map(norm)
    return (token: string): string | null => {
      const cn = norm(token)
      if (!cn) return null
      return set.find((f) => f === cn || f.endsWith('/' + cn)) ?? null
    }
  }, [files])

  return { files, reload, refresh, resolve }
}
