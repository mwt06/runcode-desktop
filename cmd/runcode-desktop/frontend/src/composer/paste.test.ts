import { describe, expect, it } from 'vitest'
import { fileNote, isImage, pastedName, sendText, shouldIntakeDrop, shouldIntakePaste, tooLargeMessage, type Attachment, type PastedItem } from './paste'

const item = (over: Partial<PastedItem> = {}): PastedItem => ({ name: 'a.txt', type: 'text/plain', size: 10, ...over })

describe('isImage', () => {
  // 按 MIME 判而不是扩展名：剪贴板里的截图没有文件名，只有 type。
  it('认 MIME 而不是扩展名', () => {
    expect(isImage(item({ name: 'shot.png', type: 'image/png' }))).toBe(true)
    expect(isImage(item({ name: 'report.png', type: 'application/pdf' }))).toBe(false)
    expect(isImage(item({ name: '', type: 'image/webp' }))).toBe(true)
  })
})

describe('pastedName', () => {
  const at = new Date(2026, 8, 3, 18, 4, 5) // 月份从 0 起：8 = 九月

  it('有真名的文件保留原名', () => {
    expect(pastedName(item({ name: '季度报告.pdf', type: 'application/pdf' }), at)).toBe('季度报告.pdf')
  })

  // 浏览器给截图的名字每次都是 image.png，一次粘几张全叫一个名。
  it('给通用名的截图换上带时间戳的名字', () => {
    expect(pastedName(item({ name: 'image.png', type: 'image/png' }), at)).toBe('截图-0903-180405.png')
    expect(pastedName(item({ name: '', type: 'image/jpeg' }), at)).toBe('截图-0903-180405.jpg')
  })

  it('没名字也没认识的 MIME 时仍给出扩展名', () => {
    expect(pastedName(item({ name: '', type: 'application/octet-stream' }), at)).toBe('粘贴文件-0903-180405.bin')
  })

  // 从网页或聊天软件复制图片时，浏览器给的名字是内容哈希。芯片上显示一长串十六进制
  // 既认不出是什么，又把整个芯片占满——实际截图里就是这样。
  it('内容哈希与时间戳这类名字也要换掉', () => {
    expect(pastedName(item({ name: '5ee44e60c73a79422db1dc0f8a3b21e7.png', type: 'image/png' }), at)).toBe('截图-0903-180405.png')
    expect(pastedName(item({ name: '1703123456789.jpg', type: 'image/jpeg' }), at)).toBe('截图-0903-180405.jpg')
    expect(pastedName(item({ name: '未命名.png', type: 'image/png' }), at)).toBe('截图-0903-180405.png')
  })

  // 别把正常名字误伤了：短的十六进制词、含哈希的正常文件名都要留着。
  it('像哈希但其实是真名的不动', () => {
    expect(pastedName(item({ name: 'abc123.png', type: 'image/png' }), at)).toBe('abc123.png')
    expect(pastedName(item({ name: '2026年度报告.pdf', type: 'application/pdf' }), at)).toBe('2026年度报告.pdf')
    expect(pastedName(item({ name: 'deadbeef-截图.png', type: 'image/png' }), at)).toBe('deadbeef-截图.png')
  })
})

describe('tooLargeMessage', () => {
  // 图片 8MB / 其它 32MB，两道闸门各自与 Go 侧对齐。
  it('图片和普通文件各按各的上限', () => {
    expect(tooLargeMessage(item({ type: 'image/png', name: 'a.png', size: 9 * 1024 * 1024 }))).toContain('图片过大(>8MB)')
    expect(tooLargeMessage(item({ type: 'image/png', name: 'a.png', size: 7 * 1024 * 1024 }))).toBe('')
    expect(tooLargeMessage(item({ type: 'application/pdf', name: 'a.pdf', size: 9 * 1024 * 1024 }))).toBe('')
    expect(tooLargeMessage(item({ type: 'application/pdf', name: 'a.pdf', size: 33 * 1024 * 1024 }))).toContain('文件过大(>32MB)')
  })
})

describe('fileNote / sendText', () => {
  // 路径用真实的 Windows 形状：附件落盘在 %AppData% 下，本应用主要跑 Windows。
  const img: Attachment = { path: String.raw`C:\pasted\shot.png`, image: true }
  const pdf: Attachment = { path: String.raw`C:\pasted\报告.pdf`, image: false }

  // 图片进多模态请求，正文里不该再重复一遍它的路径。
  it('只把非图片附件写进正文', () => {
    expect(fileNote([img])).toBe('')
    expect(fileNote([img, pdf])).toContain(String.raw`C:\pasted\报告.pdf`)
    expect(fileNote([img, pdf])).not.toContain('shot.png')
  })

  it('多个文件各占一行', () => {
    const note = fileNote([pdf, { path: String.raw`C:\pasted\b.docx`, image: false }])
    expect(note.split('\n').filter((l) => l.startsWith('- '))).toHaveLength(2)
  })

  it('正文是用户输入加附件说明', () => {
    expect(sendText('  看看这个  ', [pdf])).toBe('看看这个' + fileNote([pdf]))
    expect(sendText('只发图', [img])).toBe('只发图')
  })

  // 一个字没打、只粘了文件，就是“看看这个文件”的意思，不能发出空消息。
  it('只粘文件不打字也有正文', () => {
    expect(sendText('', [pdf]).trim()).not.toBe('')
  })
})

describe('shouldIntakePaste / shouldIntakeDrop', () => {
  // 粘贴的让步规则：有文本就走默认粘贴（从表格软件复制单元格会附带一张位图）。
  it('粘贴：有文本时让路，没文本才收文件', () => {
    expect(shouldIntakePaste(1, '')).toBe(true)
    expect(shouldIntakePaste(1, '   ')).toBe(true)
    expect(shouldIntakePaste(1, '一段表格文本')).toBe(false)
    expect(shouldIntakePaste(0, '')).toBe(false)
  })

  // 拖放没有那道让步：把文件拖进输入框这个动作本身没有歧义。
  it('拖放：带文件就收，不看有没有文本', () => {
    expect(shouldIntakeDrop(['Files'], 1)).toBe(true)
    expect(shouldIntakeDrop(['Files', 'text/plain'], 1)).toBe(true)
    expect(shouldIntakeDrop(['text/plain'], 0)).toBe(false)
    expect(shouldIntakeDrop(undefined, 1)).toBe(false)
    expect(shouldIntakeDrop(['Files'], 0)).toBe(false)
  })
})
