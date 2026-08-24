// 会后纪要：把一场录音的转写变成一条发给模型的请求。
//
// 纯函数、无副作用——这段提示词的措辞直接决定纪要的质量，而它是整个功能里最容易
// 被随手改坏的东西，所以单独拎出来测。
import type { RecordingInfo } from '@/core/bridge'


// RECORDING_MARKER 是纪要请求末尾那行机器可读的标记。
//
// 它存在的理由是「历史恢复」：对话历史由引擎回放，回放回来的只有消息原文，
// 客户端那张录音卡片本身不在里面。把这场录音的要点编进消息里，恢复时就能原地把
// 卡片重建出来——而不是让它变成一个飘在界面上、换条对话还赖着不走的浮层。
//
// 放末尾不放开头：会话标题在模型生成之前会回落到消息首行，标记占了首行的话，
// 侧栏里就是一串 JSON。
//
// 用 HTML 注释：模型会当它不存在，Markdown 渲染也不显示，而它又是消息的一部分，
// 引擎存什么就回放什么，不需要额外的存储。
const MARKER_PREFIX = '<!-- runcode-recording '
const MARKER_SUFFIX = ' -->'

/** RecordingMark 是标记里带的那点信息，够画出卡片就行。 */
export interface RecordingMark {
  id: string
  title: string
  audioMs: number
  dir?: string
  transcript?: string
  needsBackfill?: boolean
  startedAt?: string
}

export function recordingMark(info: RecordingInfo): RecordingMark {
  return {
    id: info.id, title: info.title, audioMs: info.audioMs,
    dir: info.dir, transcript: info.transcript, needsBackfill: info.needsBackfill,
    startedAt: info.startedAt,
  }
}

export function recordingMarker(mark: RecordingMark): string {
  return MARKER_PREFIX + JSON.stringify(mark) + MARKER_SUFFIX
}

/**
 * parseRecordingMarker 从一条消息里认出录音标记。
 * 认不出返回 null——历史里绝大多数消息都不是纪要请求，这条路要便宜。
 */
export function parseRecordingMarker(text: string): RecordingMark | null {
  const at = text.indexOf(MARKER_PREFIX)
  if (at < 0) return null
  const end = text.indexOf(MARKER_SUFFIX, at)
  if (end < 0) return null
  try {
    const mark = JSON.parse(text.slice(at + MARKER_PREFIX.length, end)) as RecordingMark
    return mark && typeof mark.id === 'string' && mark.id ? mark : null
  } catch {
    // 标记坏了就当没有：宁可少画一张卡片，也不能让一条历史消息渲染不出来。
    return null
  }
}
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
export function minutesFileName(mark: RecordingMark): string {
  const info = mark
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

function clock(iso: string | undefined): string {
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
  mark: RecordingMark
  transcript: string
  skill?: string
  outPath: string
}): string {
  const { mark, transcript, skill, outPath } = opts
  const info = mark
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
  for (const line of speakerBriefing(transcript)) lines.push(line)
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

  // 标记放**末尾**。放首行的话，会话标题在模型生成之前会回落到消息首行，
  // 侧栏里就是一串 `<!-- runcode-recording {"id":…` 的 JSON —— 实测踩过。
  lines.push('')
  lines.push(recordingMarker(mark))

  return lines.join('\n')
}

// SPEAKER_RE 从转写里抠出说话人标签。落盘格式是 `**[00:03] S1**：文本`
// （见 Go 侧 Transcript.Markdown），两边必须对上。
const SPEAKER_RE = /^\*\*\[[^\]]+\]\s*([^*]+?)\*\*：/gm

/** speakerLabels 列出这份转写里实际出现过的说话人标签，按首次出现排序。 */
export function speakerLabels(transcript: string): string[] {
  const seen: string[] = []
  for (const m of transcript.matchAll(SPEAKER_RE)) {
    const label = m[1].trim()
    if (label && !seen.includes(label)) seen.push(label)
  }
  return seen
}

/**
 * speakerBriefing 把「这些标签到底是什么」讲清楚，不让模型去猜。
 *
 * 光说一句「S1/S2 是声纹编号」不够，实测模型仍会去猜编号和转写里提到的人名之间
 * 的对应关系（「S1 应该就是阿波哥」）。所以这里做三件事：
 *  - 列出本场实际出现的标签，而不是举例说「S1／S2 这类」；
 *  - 说明编号的**作用域**：同一场内同号即同人，跨场不通用，号大小没有含义；
 *  - 明确切断「内容里提到的人名」与「编号」之间的联系——那是这类纪要最容易出的
 *    硬错误，一旦把发言安到具体某人头上，纪要就成了会害人的东西。
 */
export function speakerBriefing(transcript: string): string[] {
  const labels = speakerLabels(transcript)
  const clustered = labels.filter((l) => /^S\d+$/.test(l))
  const named = labels.filter((l) => !/^S\d+$/.test(l))

  const out: string[] = []
  out.push(`- 本场出现的说话人标签：${labels.length ? labels.join('、') : '（无）'}`)
  if (clustered.length) {
    out.push(
      `- ${clustered.join('、')} 是声纹聚类给出的编号，**不是姓名**。` +
      '同一个编号在本场内指同一个人；编号的数字本身没有含义，也不在不同场次之间通用。',
    )
  }
  if (named.length) {
    out.push(`- ${named.join('、')} 是配置里填的显示名，对应本机麦克风录到的人。`)
  }
  out.push(
    '- 转写内容里提到的人名（例如有人喊了一声某某），**不能**据此认定某个编号就是那个人——' +
    '喊名字的和被喊的从来不是同一个人。除非某个编号自报姓名，否则一律保留编号，' +
    '并在需要时注明「无法确认对应哪位」。',
  )
  return out
}

/** minutesDisplayText 是对话里代替整篇提示词显示的那一句。实时与恢复共用，两边必须一致。 */
export function minutesDisplayText(title: string): string {
  return `录音纪要 · 已附上《${title || '录音'}》的转写全文`
}
