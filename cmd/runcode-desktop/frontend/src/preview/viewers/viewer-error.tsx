import { openExternal } from '@/core/bridge'

// 三个 Office 查看器(docx/xlsx/pptx)共用的失败提示：说明原因，并给一条「交给系统
// 程序打开」的退路——浏览器内渲染失败不代表文件本身有问题。
export function ViewerError({ relPath, message }: { relPath: string; message: string }) {
  return (
    <div className="p-6 text-[13px] text-red">预览失败：{message}
      <button className="text-primaryink underline ml-1" onClick={() => openExternal(relPath).catch(() => {})}>用系统程序打开</button>
    </div>
  )
}
