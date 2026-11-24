// 录音纪要设置：转写服务地址、我的显示名、两路设备、是否保留本地音频。
// 完全自包含——读写都直接落 recorder.json，与会话配置无关。
//
// 实时总结模型这一项后端已经有了（RecorderSettings.summaryModel），但总结本身
// 还没接上，所以界面上先不放：一个设了不起作用的下拉，比没有更让人困惑。
import { useEffect, useRef, useState } from 'react'
import { FIELD_CLS, SelectField } from '@/ui/fields'
import { Toggle } from '@/ui/toggle'
import { InlineError } from '@/ui/feedback'
import {
  errText, patchRecorderSettings, recorderDevices, recorderSettings,
  type RecorderDeviceList, type RecorderSettings, type RecorderSettingsPatch,
} from '@/core/bridge'
import { Section } from './section'
import { createAutoSaveChannel, type AutoSaveChannel, type AutoSavePhase } from './autosave-channel'
import { useSettingsSaveRegistry } from './settings-save-provider'

// version 是后端独占的配置迁移版本，界面上没有对应控件；这里给 0 只是占位，
// 保存时后端会盖成当前值（见 desktop.SaveRecorderSettings）。
const EMPTY: RecorderSettings = {
  gatewayUrl: '', speakerName: '', lang: '', version: 0,
  micDeviceId: '', sysDeviceId: '', keepAudio: true, summaryModel: '', gatewayToken: '', micDiarize: false,
  autoFullMinutes: false,
}

type RecorderField =
  | 'gatewayUrl' | 'gatewayToken' | 'speakerName'
  | 'micDeviceId' | 'sysDeviceId'
  | 'micDiarize' | 'keepAudio' | 'autoFullMinutes'

export function RecorderSection() {
  const registry = useSettingsSaveRegistry()
  const [s, setS] = useState<RecorderSettings>(EMPTY)
  const [devices, setDevices] = useState<RecorderDeviceList | null>(null)
  const [error, setError] = useState('')
  const channels = useRef(new Map<RecorderField, AutoSaveChannel<{ value: string | boolean }>>())
  const unsubscribes = useRef<Array<() => void>>([])
  const alive = useRef(true)
  const [autoPhase, setAutoPhase] = useState<AutoSavePhase>('idle')
  const [autoError, setAutoError] = useState('')

  useEffect(() => {
    recorderSettings().then(setS).catch((e: unknown) => setError(errText(e)))
    recorderDevices().then(setDevices).catch((e: unknown) => setError(errText(e)))
    return () => {
      alive.current = false
      for (const unsubscribe of unsubscribes.current) unsubscribe()
      unsubscribes.current = []
    }
  }, [])

  const syncAutoStatus = () => {
    if (!alive.current) return
    const all = Array.from(channels.current.values(), (channel) => channel.getSnapshot())
    const failed = all.find((snapshot) => snapshot.phase === 'error' || snapshot.phase === 'invalid')
    const saving = all.some((snapshot) => snapshot.phase === 'saving')
    const saved = all.some((snapshot) => snapshot.phase === 'saved')
    setAutoPhase(failed?.phase ?? (saving ? 'saving' : saved ? 'saved' : 'idle'))
    setAutoError(failed?.error ?? '')
  }

  const fieldChannel = (field: RecorderField, initial: string | boolean) => {
    const existing = channels.current.get(field)
    if (existing) return existing
    const channel = createAutoSaveChannel<{ value: string | boolean }>({
      initial: { value: initial },
      delayMs: 600,
      write: async ({ value }) => {
        await patchRecorderSettings({ [field]: value } as RecorderSettingsPatch)
        return { value }
      },
    })
    registry.register(`recorder:${field}`, '录音设置', channel)
    unsubscribes.current.push(channel.subscribe(syncAutoStatus))
    channels.current.set(field, channel)
    return channel
  }

  const patch = (field: RecorderField, value: string | boolean, mode: 'immediate' | 'debounced') => {
    const previous = s[field]
    setS((cur) => ({ ...cur, [field]: value }))
    const channel = fieldChannel(field, previous)
    channel.edit({ value }, mode)
  }

  const flush = (field: RecorderField) => {
    void channels.current.get(field)?.flush()
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
        onChange={(e) => patch('gatewayUrl', e.target.value, 'debounced')}
        onBlur={() => flush('gatewayUrl')}
      />
      <p className="text-[12px] text-faint -mt-1.5">
        清空保存会退回内置的默认地址。断线期间的音频按设计直接丢弃、不重放，所以断过的那一段在
        服务端没有文字——录完会在卡片上标出来，本地音频还在，可以之后补一次。
      </p>

      <label className="text-[12px] text-muted">访问令牌</label>
      <input
        className={FIELD_CLS}
        type="password"
        placeholder="服务端的 FUNASR_AUTH_TOKEN"
        value={s.gatewayToken}
        onChange={(e) => patch('gatewayToken', e.target.value, 'debounced')}
        onBlur={() => flush('gatewayToken')}
      />
      <p className="text-[12px] text-faint -mt-1.5">
        它认的是「你能不能用这台转写服务」，与登录用的通行证是两回事。清空保存会退回
        内置的默认令牌；填错或服务端不认，界面上表现为「离线录制中」。
      </p>

      <label className="text-[12px] text-muted">我的显示名</label>
      <input
        className={FIELD_CLS}
        placeholder="留空则用通行证里的姓名"
        value={s.speakerName}
        onChange={(e) => patch('speakerName', e.target.value, 'debounced')}
        onBlur={() => flush('speakerName')}
      />

      <label className="text-[12px] text-muted">麦克风</label>
      <SelectField value={s.micDeviceId} onChange={(v) => patch('micDeviceId', v, 'immediate')} disabled={unsupported}>
        <option value="">系统默认</option>
        {(devices?.mic ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
      </SelectField>

      <label className="text-[12px] text-muted">系统声音（会议软件里对方的声音）</label>
      <SelectField value={s.sysDeviceId} onChange={(v) => patch('sysDeviceId', v, 'immediate')} disabled={unsupported}>
        <option value="">系统默认</option>
        {(devices?.sys ?? []).map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
      </SelectField>

      <label className="flex items-center justify-between gap-3 cursor-pointer">
        <span className="min-w-0">
          <span className="text-[13px]">麦克风也区分说话人</span>
          <span className="block text-[12px] text-muted mt-0.5">
            默认开着，几个人围着一台电脑面对面开会要靠它，否则整场会被标成你一个人。
            纯线上会议可以关掉——对方的声音走「系统声音」那一路，那边一直开着。
            关掉之后你自己说的话会直接标上姓名；开着时服务端只给 S1、S2 这样的聚类
            编号，认不出是谁。单麦分离的准确率也明显低于双轨，而且要多花 GPU。
          </span>
        </span>
        <Toggle on={s.micDiarize} onChange={(v) => patch('micDiarize', v, 'immediate')} />
      </label>

      <label className="flex items-center justify-between gap-3 cursor-pointer">
        <span className="min-w-0">
          <span className="text-[13px]">保留本地音频</span>
          <span className="block text-[12px] text-muted mt-0.5">
            转写有缺口时，本地 WAV 是唯一能补回来的依据。双轨约 230 MB/小时。
          </span>
        </span>
        <Toggle on={s.keepAudio} onChange={(v) => patch('keepAudio', v, 'immediate')} />
      </label>

      <label className="flex items-center justify-between gap-3 cursor-pointer">
        <span className="min-w-0">
          <span className="text-[13px]">录完直接出纪要文档</span>
          <span className="block text-[12px] text-muted mt-0.5">
            默认关着：录完先给一份速览——主题、总结、带时间戳的大纲和待办，一屏看完，
            再由你决定要不要出正式文档。每场都要文档的话打开它，省掉中间这一步。
          </span>
        </span>
        <Toggle on={s.autoFullMinutes} onChange={(v) => patch('autoFullMinutes', v, 'immediate')} />
      </label>

      {(autoPhase === 'saving' || autoPhase === 'saved' || autoPhase === 'error' || autoPhase === 'invalid') && (
        <div role="status" aria-live="polite" className={`text-[12px] ${autoPhase === 'error' || autoPhase === 'invalid' ? 'text-red' : 'text-muted'}`}>
          {autoPhase === 'saving' ? '正在自动保存…'
            : autoPhase === 'error' || autoPhase === 'invalid' ? `未保存：${autoError}`
              : '已自动保存'}
        </div>
      )}
      <InlineError>{error}</InlineError>
    </Section>
  )
}
