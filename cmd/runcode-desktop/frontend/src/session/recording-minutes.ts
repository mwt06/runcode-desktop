import type { RecordingInfo, SkillList } from '@/core/bridge'
import {
  buildDigestPrompt, buildMinutesPrompt, digestDisplayText, docDisplayText,
  minutesFileName, pickMinutesSkill, type MinutesStage, type RecordingMark,
} from '@/recorder/minutes'

export interface MinutesActions {
  read: (id: string) => Promise<string>
  skills: () => Promise<SkillList>
  send: (text: string, attachments: string[], display: string) => Promise<void>
  notify: (message: string) => void
  errorText: (error: unknown) => string
}

export async function sendRecordingMinutes(mark: RecordingMark, stage: MinutesStage, current: () => boolean, deps: MinutesActions) {
  if (!mark.id) return
  if (!current()) {
    deps.notify('还没有进行中的对话，无法生成纪要')
    return
  }
  try {
    const text = await deps.read(mark.id)
    if (!current()) return
    if (!text.trim()) { deps.notify('这场录音没有转写文字，生成不了纪要'); return }
    if (stage === 'digest') {
      await deps.send(buildDigestPrompt({ mark, transcript: text }), [], digestDisplayText(mark.title))
      return
    }
    const list = await deps.skills().catch(() => null)
    if (!current()) return
    const usable = (list?.skills ?? []).filter((s) => !s.disabledUser && !s.disabledProject)
    const skill = pickMinutesSkill(usable.map((s) => s.name))
    await deps.send(buildMinutesPrompt({ mark, transcript: text, skill, outPath: minutesFileName(mark) }), [], docDisplayText(mark.title))
  } catch (e) { deps.notify(deps.errorText(e)) }
}

// 按录音 id 去重，无会话时不消耗这次机会。
export function createMinutesTrigger() {
  const fired = new Set<string>()
  return (rec: RecordingInfo | null, hasSession: boolean): rec is RecordingInfo => {
    if (!hasSession || !rec || rec.state !== 'stopped' || !rec.id || !rec.transcript || fired.has(rec.id)) return false
    fired.add(rec.id)
    return true
  }
}
