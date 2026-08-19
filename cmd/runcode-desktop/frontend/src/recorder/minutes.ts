// 会后纪要：把一场录音的转写变成一条发给模型的请求。
//
// 纯函数、无副作用——这段提示词的措辞直接决定纪要的质量，而它是整个功能里最容易
// 被随手改坏的东西，所以单独拎出来测。
import type { RecordingInfo } from '@/core/bridge'

// SKILL_HINTS 是用来认「会议纪要」类技能的关键词。命中就让模型走那个技能，
// 拿到的是机构自己的模板，而不是模型即兴发挥的格式。
const SKILL_HINTS = ['会议纪要', '纪要', 'minutes']

/** pickMinutesSkill 从技能列表里挑一个像「会议纪要」的。挑不到返回空串。 */
export function pickMinutesSkill(names: string[]): string {
  for (const hint of SKILL_HINTS) {
    const hit = names.find((n) => n.includes(hint))
    if (hit) return hit
  }
  return ''
}

/** minutesFileName 是纪要落盘的文件名。日期放前面，一个工作区里多场会议才排得整齐。 */
export function minutesFileName(info: RecordingInfo): string {
  const d = info.startedAt ? new Date(info.startedAt) : new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const stamp = `${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}`
  // 文件名里不能出现路径分隔符与 Windows 保留字符，标题是用户填的，得洗一遍。
  const title = (info.title || '录音纪要').replace(/[\\/:*?"<>|]/g, '_').slice(0, 40)
  return `会议纪要-${stamp}-${title}.md`
}

function human(ms: number): string {
  const total = Math.round(Math.max(0, ms) / 1000)
  const m = Math.floor(total / 60)
  const s = total % 60
  return m > 0 ? `${m} 分 ${s} 秒` : `${s} 秒`
}

function clock(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/**
 * buildMinutesPrompt 组装发给模型的那条消息。
 *
 * 几处刻意的措辞，都是针对语音转写这个输入的特点：
 *  - 明说文本来自自动识别、可能有错别字与断句问题，否则模型会把明显的同音错误
 *    当成事实照抄进纪要；
 *  - 明说 S1/S2 是声纹聚类编号不是人名，否则纪要里会冒出「S1 表示……」这种句子；
 *  - 明说「转写里没有的信息不要补」——纪要被当成会议记录用，补出来的待办和责任人
 *    是会真的害到人的那类错误。
 */
export function buildMinutesPrompt(opts: {
  info: RecordingInfo
  transcript: string
  skill?: string
  outPath: string
}): string {
  const { info, transcript, skill, outPath } = opts
  const lines: string[] = []

  if (skill) {
    lines.push(`请使用「${skill}」技能，按它的模板整理下面这场会议的纪要。`)
  } else {
    lines.push('请把下面这场会议的录音转写整理成一份会议纪要。')
  }
  lines.push('')

  lines.push(`- 标题：${info.title || '录音纪要'}`)
  const started = clock(info.startedAt)
  if (started) lines.push(`- 时间：${started}`)
  lines.push(`- 时长：${human(info.audioMs)}`)
  lines.push('- 说话人：「我」是本机麦克风录到的，其余来自会议软件；S1／S2 这类编号是声纹聚类结果，不是真名，除非转写里出现了姓名，否则不要替它们编造身份。')
  if (info.needsBackfill) {
    lines.push('- 注意：这场录音与转写服务断开过，文本有缺口，中间可能整段缺失。纪要里如果发现话题接不上，直接说明可能有缺失，不要脑补衔接。')
  }
  lines.push('')

  lines.push('要求：')
  lines.push('1. 先通读全文再落笔，按议题归纳，不要逐句复述。')
  lines.push('2. 结论、待办、责任人、时间点分别列出。**转写里没有的信息一律不要补**——这份纪要会被当作会议记录用，编出来的待办和责任人是会真害到人的。')
  lines.push('3. 转写来自自动识别，有错别字和断句问题。按上下文改正明显的同音错误；改动大的地方在括号里附上原文。')
  lines.push(`4. 写成 Markdown，保存到工作区的 \`${outPath}\`，然后简要说明纪要包含哪几部分。`)
  lines.push('')

  lines.push('转写全文：')
  lines.push('')
  lines.push('```')
  lines.push(transcript.trim())
  lines.push('```')

  return lines.join('\n')
}
