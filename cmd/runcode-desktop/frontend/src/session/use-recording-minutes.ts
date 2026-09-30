import { useCallback, useEffect, useRef } from 'react'
import { errText, listSkills, readRecordingTranscript, recorderSettings, type RecordingInfo } from '@/core/bridge'
import { recordingMark, type MinutesStage, type RecordingMark } from '@/recorder/minutes'
import { createMinutesTrigger, sendRecordingMinutes } from './recording-minutes'
import { useActionScope } from './use-action-scope'

export function useRecordingMinutes({ sessionId, recording, send, pushRecording, notify }: {
  sessionId: string
  recording: RecordingInfo | null
  send: (text: string, attachments: string[], display: string) => Promise<void>
  pushRecording: (mark: RecordingMark) => void
  notify: (message: string) => void
}) {
  const scope = useActionScope(sessionId)
  const trigger = useRef(createMinutesTrigger())
  // 同一会话等待转写时可能已开始另一个回合，发送要用最新的 busy/插入分支。
  const latest = useRef({ send, notify })
  latest.current = { send, notify }
  const sendRecordingRequest = useCallback((mark: RecordingMark, stage: MinutesStage) =>
    sendRecordingMinutes(mark, stage, scope.capture(), {
      read: readRecordingTranscript, skills: listSkills,
      send: (...args) => latest.current.send(...args), notify: (text) => latest.current.notify(text), errorText: errText,
    }), [scope])

  useEffect(() => {
    if (!trigger.current(recording, !!sessionId)) return
    const current = scope.capture()
    const mark = recordingMark(recording)
    pushRecording(mark)
    void (async () => {
      // 现读设置，失败默认出轻量速览。
      const auto = await recorderSettings().then((s) => s.autoFullMinutes).catch(() => false)
      if (!current()) return
      await sendRecordingRequest(mark, auto ? 'doc' : 'digest')
    })()
  }, [recording, sessionId, scope, pushRecording, sendRecordingRequest])
  return { sendRecordingRequest }
}
