// 粘贴进输入框的附件——纯逻辑部分：从粘贴事件里挑出哪些东西该当附件收下、
// 该给它起什么名字、以及这批附件最后怎么变成发给模型的那段文字。
// 与 React 和 DOM 都无关（只依赖 name/type/size 三个字段），可单测。

// PastedItem 是本模块认得的最小形状。浏览器给的 File 天然满足它，测试里造个普通
// 对象也满足——这就是不直接写 File 的原因。
export interface PastedItem {
  name: string
  type: string
  size: number
}

// IMAGE_LIMIT 是图片附件的字节上限，与 Go 侧 maxImageBytes 一致（8 MiB）。
//
// 为什么前端也要判一次：超限的图片在**发送**时才会被 loadImages 拒掉，那时用户
// 早就把附件加进去、把话打完了，报错来得太晚。这里在粘贴那一刻就说清楚。
export const IMAGE_LIMIT = 8 * 1024 * 1024

// FILE_LIMIT 是非图片附件的字节上限，与 Go 侧 maxPastedBytes 一致（32 MiB）。
export const FILE_LIMIT = 32 * 1024 * 1024

// isImage 按 MIME 判断，不看扩展名：截图粘进来时压根没有文件名（见 pastedName），
// 有的只有 type。
export const isImage = (f: PastedItem): boolean => f.type.startsWith('image/')

// sizeLimit 是这个附件该受哪道闸门。
export const sizeLimit = (f: PastedItem): number => (isImage(f) ? IMAGE_LIMIT : FILE_LIMIT)

// tooLargeMessage 返回超限提示；没超限返回 ''。
//
// 措辞跟 Go 侧对齐（“图片过大(>8MB)”），这样同一件事在粘贴时和发送时说法一致，
// 用户不会以为碰上了两个不同的问题。
export function tooLargeMessage(f: PastedItem): string {
  const limit = sizeLimit(f)
  if (f.size <= limit) return ''
  const mb = Math.round(limit / 1024 / 1024)
  return isImage(f) ? `图片过大(>${mb}MB)：${pastedName(f)}` : `文件过大(>${mb}MB)：${pastedName(f)}`
}

// EXT_BY_MIME 覆盖截图会用到的几种位图格式。剪贴板里的截图是一段没有名字的位图，
// 名字得由我们自己起。
const EXT_BY_MIME: Record<string, string> = {
  'image/png': '.png',
  'image/jpeg': '.jpg',
  'image/gif': '.gif',
  'image/webp': '.webp',
  'image/bmp': '.bmp',
  'image/svg+xml': '.svg',
}

// GENERIC_STEM 认得出「这不是人起的名字」。命中的一律换掉。
//
// 哈希那条是从真实截图里看出来的：从网页或聊天软件复制一张图，浏览器交给页面的
// File.name 往往是内容哈希（形如 5ee44e60c73a79422db1dc…），附件芯片上于是就是
// 一长串十六进制——既看不出是什么，又长得占满整个芯片。
const GENERIC_STEM: RegExp[] = [
  /^image$/i, // 浏览器给剪贴板位图的默认名
  /^[0-9a-f]{16,}$/i, // 内容哈希
  /^\d{10,}$/, // 毫秒时间戳
  /^(未命名|无标题|下载|untitled|download|blob)$/i,
]

// isGenericName 判断这个名字要不要被换掉（空名、以及上面那几种形状）。
function isGenericName(name: string): boolean {
  const stem = name.replace(/\.[^.]*$/, '').trim()
  return stem === '' || GENERIC_STEM.some((re) => re.test(stem))
}

// pastedName 给附件起一个体面的文件名。
//
// 有真名的（从资源管理器复制/拖来的文件）原样保留——那是用户认得的东西。名字没有
// 信息量的换成带时间戳的名字：它会出现在附件芯片和对话记录里，"截图-0903-181530.png"
// 比一串哈希、或者三个一模一样的 "image.png" 好认得多。
export function pastedName(f: PastedItem, at: Date = new Date()): string {
  if (!isGenericName(f.name)) return f.name
  const ext = EXT_BY_MIME[f.type] ?? (f.name.includes('.') ? f.name.slice(f.name.lastIndexOf('.')) : '.bin')
  return `${isImage(f) ? '截图' : '粘贴文件'}-${stamp(at)}${ext}`
}

// stamp 是本地时间的 MMDD-HHmmss，够一次会话里区分先后，又不至于长得占满芯片。
function stamp(at: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(at.getMonth() + 1)}${p(at.getDate())}-${p(at.getHours())}${p(at.getMinutes())}${p(at.getSeconds())}`
}

// Attachment 是输入框里挂着的一个附件：落盘后的绝对路径 + 它是不是图片。
//
// 这两类的去向完全不同，所以类型必须一路带着：图片进多模态请求（模型直接看图），
// 其它文件只能把路径写进消息里，让模型自己用 Read/ReadOffice 去读。
export interface Attachment {
  path: string
  image: boolean
}

// fileNote 把非图片附件拼成一段附在消息末尾的说明，没有则返回 ''。
//
// 为什么要写路径而不是只写文件名：模型要能真的读到它。这些文件落在应用数据目录下，
// 那里有常驻访问权（见 Go 侧 appdirs），所以模型读它不会再弹一次“项目外授权”。
export function fileNote(attachments: Attachment[]): string {
  const files = attachments.filter((a) => !a.image)
  if (files.length === 0) return ''
  const lines = files.map((a) => `- ${a.path}`).join('\n')
  return `\n\n用户粘贴了以下文件，需要时请读取：\n${lines}`
}

// sendText 是最终发给模型的正文：用户打的字 + 非图片附件的路径说明。
// 用户只粘了文件、一个字没打时也成立——那就是“看看这个文件”的意思。
export function sendText(input: string, attachments: Attachment[]): string {
  return input.trim() + fileNote(attachments)
}

// shouldIntakePaste / shouldIntakeDrop 是"这次操作该不该收成附件"的判定。
//
// 两者规则**故意不同**，差别只在一处：粘贴时剪贴板里同时躺着文本和一张位图是常态
// （从表格软件复制一片单元格就是这样），那种时候用户要的显然是文本，所以有文本就
// 让路，走浏览器的默认粘贴。而把文件拖进输入框这个动作本身没有歧义，没什么好让的。
export function shouldIntakePaste(fileCount: number, text: string): boolean {
  return fileCount > 0 && text.trim() === ''
}

// shouldIntakeDrop 要求拖拽里确实带着文件。只看 files 长度不够：拖一段选中的文字
// 进来时 files 是空的，但拖一个浏览器里的图片链接可能两者都有，types 才是权威。
export function shouldIntakeDrop(types: readonly string[] | undefined, fileCount: number): boolean {
  return fileCount > 0 && !!types?.includes('Files')
}
