// 录音纪要设置：转写服务地址、我的显示名、两路设备、是否保留本地音频。
// 完全自包含——读写都直接落 recorder.json，与会话配置无关。
//
// 实时总结模型这一项后端已经有了（RecorderSettings.summaryModel），但总结本身
// 还没接上，所以界面上先不放：一个设了不起作用的下拉，比没有更让人困惑。
import { useEffect, useState } from 'react'
import { BTN } from '@/ui/tokens'
import { FIELD_CLS, SelectField } from '@/ui/fields'
import { Toggle } from '@/ui/toggle'
import { InlineError } from '@/ui/feedback'
import {
  errText, recorderDevices, recorderSettings, saveRecorderSettings,
  type RecorderDeviceList, type RecorderSettings,
} from '@/core/bridge'
import { Section } from './section'

const EMPTY: RecorderSettings = {
  gatewayUrl: '', speakerName: '', lang: '',
  micDeviceId: '', sysDeviceId: '', keepAudio: true, summaryModel: '',
}

export function RecorderSection() {
  const [s, setS] = useState<RecorderSettings>(EMPTY)
  const [devices, setDevices] = useState<RecorderDeviceList | null>(null)
  const [msg, setMsg] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    recorderSettings().then(setS).catch((e: unknown) => setError(errText(e)))
    recorderDevices().then(setDevices).catch((e: unknown) => setError(errText(e)))
  }, [])

  const patch = (p: Partial<RecorderSettings>) => {
    setS((cur) => ({ ...cur, ...p }))
    setMsg('')
  }

  const save = async () => {
    setError('')
    try {
      await saveRecorderSettings(s)
      setMsg('已保存')
    } catch (e) {
      setError(errText(e))
    }
  }

  const unsupported = devices !== null && !devices.supported

  return (
    <Section title="录音纪要" hint="双轨录音 · 麦克风 + 系统声音">
      {unsupported && (
        <p className="text-[12px] text-amberink -mt-1.5">这台机器上开不了录音：{devices?.reason}</p>
      )}

      <label className="text-[12px] text-muted">转写服务地址</label>
      <input
        className={FIELD_CLS}
        placeholder="ws://<主机>:8000/ws（或 wss://）"
        value={s.gatewayUrl}
        onChange={(e) => patch({ gatewayUrl: e.target.value })}
      />
      <p className="text-[12px] text-faint -mt-1.5">
        没填就不能开始录音。断线期间的音频按设计直接丢弃、不重放，所以断过的那一段在
        服务端没有文字——录完会在卡片上标出来，本地音频还在，可以之后补一次。
      </p>

      <label className="text-[12px] text-muted">我的显示名</label>
      <input
        className={FIELD_CLS}
        placeholder="留空则用通行证里的姓名"
        value={s.speakerName}
        onChange={(e) => patch({ speakerName: e.target.value })}
      />

      <label className="text-[12px] text-muted">麦克风</label>
      <SelectField value={s.micDeviceId} onChange={(v) => patch({ micDeviceId: v })} disabled={unsupported}>
        <option value="">系统默认</option>
        {(devices?.mic ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
      </SelectField>

      <label className="text-[12px] text-muted">系统声音（会议软件里对方的声音）</label>
      <SelectField value={s.sysDeviceId} onChange={(v) => patch({ sysDeviceId: v })} disabled={unsupported}>
        <option value="">系统默认</option>
        {(devices?.sys ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
      </SelectField>

      <label className="flex items-center justify-between gap-3 cursor-pointer">
        <span className="min-w-0">
          <span className="text-[13px]">保留本地音频</span>
          <span className="block text-[12px] text-muted mt-0.5">
            转写有缺口时，本地 WAV 是唯一能补回来的依据。双轨约 230 MB/小时。
          </span>
        </span>
        <Toggle on={s.keepAudio} onChange={(v) => patch({ keepAudio: v })} />
      </label>

      <div className="flex items-center gap-2">
        <button type="button" className={`${BTN} px-5`} onClick={() => void save()}>保存</button>
        {msg && <span className="text-[12px] text-muted">{msg}</span>}
      </div>
      <InlineError>{error}</InlineError>
    </Section>
  )
}
