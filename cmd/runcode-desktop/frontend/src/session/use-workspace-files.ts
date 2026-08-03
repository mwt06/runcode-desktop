import { useCallback, useMemo, useRef, useState } from 'react'
import { listFiles } from '@/core/bridge'
import { toWorkspaceRel } from '@/preview/classify'

export type WorkspaceFiles = ReturnType<typeof useWorkspaceFiles>

// 工作区文件清单：# 引用选择器、文件浏览器与回复产物匹配共用一份。
// reload 与 refresh 的差别是刻意的：切会话时列表必须换成新工作区的（失败就清空，
// 免得把上一个工作区的文件留在界面上），而回合结束的增量刷新失败时保留旧值，
// 一次抖动不该让整列表消失。
//
// getCwd 取当前工作区根：模型在正文里报路径时经常写**绝对路径**（"已生成
// D:\演示\projects\…\deck.pptx"），而清单里存的是工作区相对路径，两者直接比永远
// 对不上——那行字于是不可点。先换算再匹配即可。
//
// 之所以传取值函数而不是字符串：本钩子在会话状态建立之前就要创建（会话依赖它的
// refresh），拿不到当时还不存在的 cwd；存进 ref 后按调用时读，既拿得到最新值，
// 又不会让下面那个 memo 每次渲染都重算整份清单。
export function useWorkspaceFiles(getCwd: () => string) {
  const [files, setFiles] = useState<string[]>([])
  const cwdRef = useRef(getCwd)
  cwdRef.current = getCwd

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

  // resolve maps a token written by the model (an absolute path, a bare name, or a
  // partial path) onto an actual workspace file, so a reply's file mention can
  // become a link. An absolute path under the workspace is converted first —
  // toWorkspaceRel also handles the case-insensitive prefix match Windows needs.
  const resolve = useMemo(() => {
    const norm = (s: string) => s.replace(/\\/g, '/').replace(/^\.\//, '').replace(/^\/+/, '')
    const set = files.map(norm)
    return (token: string): string | null => {
      const cn = norm(toWorkspaceRel(token, cwdRef.current()))
      if (!cn) return null
      return set.find((f) => f === cn || f.endsWith('/' + cn)) ?? null
    }
  }, [files])

  return { files, reload, refresh, resolve }
}
