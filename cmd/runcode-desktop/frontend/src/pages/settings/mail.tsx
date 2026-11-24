// 设置 → 账号与登录 → 邮箱：5 步首次接入向导。不复用 automation 表单。
import { useEffect, useId, useRef, useState } from 'react'
import {
  confirmMailTestReceipt, errText, getMailSettings, openMailOfficialURL, saveMailAccount,
  sendMailTestToSelf, testMailConnection, MailOnboardingStatuses, MailVersions,
  type MailAccountView, type MailConnectionTestResult, type MailSettings, type MailVersion,
} from '@/core/bridge'
import {
  canAdvanceFromStep1, initialWizardStep, mailFormFingerprint, mailHostForVersion,
  mailTestStillValid, mailWebmailPage, type MailFormDraft, type MailWizardStep,
} from '@/core/mail-settings'
import { BTN, BTN_PRIMARY } from '@/ui/tokens'
import { Banner, InlineError } from '@/ui/feedback'
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { Section } from './section'

const STEP_TITLES = ['确认账号', '网页生成安全密码', '填写并测试', '安全保存', '自测收信'] as const

const PASSWORD_CHECKLIST = [
  '在邮箱网页右上角点设置',
  '点查看更多设置',
  '左侧进入账户与安全 → 账户安全',
  '找到三方客户端登录安全管理',
  '若三方客户端安全密码关闭则开启；若已开启且无法关闭，保持开启',
  '点生成新密码，本人完成手机验证码',
  '复制网页显示的密码（通常只显示一次）',
  '设备用途填写「智开－本机电脑名称」并确定',
] as const

export function MailSection() {
  const [settings, setSettings] = useState<MailSettings | null>(null)
  const [loadError, setLoadError] = useState('')
  const [step, setStep] = useState<MailWizardStep>(1)
  const [draft, setDraft] = useState<MailFormDraft>({
    address: '', displayName: '', version: MailVersions.Standard, password: '',
  })
  const [showPassword, setShowPassword] = useState(false)
  const [testResult, setTestResult] = useState<MailConnectionTestResult | null>(null)
  const [testedFingerprint, setTestedFingerprint] = useState<string | null>(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [sending, setSending] = useState(false)
  const [sendMessage, setSendMessage] = useState('')
  const [sendError, setSendError] = useState('')
  const [shortId, setShortId] = useState('')
  const [guiding, setGuiding] = useState(false)
  const passwordRef = useRef('')
  const [generation] = useState(() => ({ seq: 0 }))
  const clientReq = useId()

  useEffect(() => {
    const g = ++generation.seq
    void getMailSettings().then((s) => {
      if (g !== generation.seq) return
      setSettings(s)
      const acc = s.account
      if (acc) {
        setDraft({
          address: acc.address, displayName: acc.displayName, version: (acc.version as MailVersion) || MailVersions.Standard, password: '',
        })
        setStep(initialWizardStep(true, acc.onboardingStatus))
        setShortId(acc.lastTestShortId ?? '')
      }
    }).catch((e) => {
      if (g !== generation.seq) return
      setLoadError(errText(e) || '未能读取邮箱设置')
    })
    return () => {
      generation.seq++
      // 卸载时丢弃内存中的明文密码，不写 localStorage。
      passwordRef.current = ''
      setDraft((d) => ({ ...d, password: '' }))
    }
  }, [generation])

  function patchDraft(patch: Partial<MailFormDraft>) {
    setDraft((d) => {
      const next = { ...d, ...patch }
      if ('address' in patch || 'version' in patch || 'password' in patch) {
        setTestResult(null)
        setTestedFingerprint(null)
        setSaveError('')
      }
      if ('password' in patch) passwordRef.current = patch.password ?? ''
      return next
    })
  }

  const testValid = mailTestStillValid(testedFingerprint, draft)
  const account = settings?.account
  const secretsOk = settings?.secretStorageOk !== false

  async function runTest() {
    if (testing) return
    setTesting(true)
    setSaveError('')
    try {
      const res = await testMailConnection({
        address: draft.address, password: draft.password, version: draft.version,
      })
      setTestResult(res)
      if (res.ok && res.configFingerprint) {
        setTestedFingerprint(mailFormFingerprint(draft))
      } else {
        setTestedFingerprint(null)
      }
    } catch (e) {
      setTestResult({ ok: false, message: errText(e) || '连接测试失败' })
      setTestedFingerprint(null)
    } finally {
      setTesting(false)
    }
  }

  async function runSave() {
    if (saving || !testValid || !testResult?.configFingerprint) return
    setSaving(true)
    setSaveError('')
    try {
      const next = await saveMailAccount({
        address: draft.address,
        displayName: draft.displayName,
        version: draft.version,
        password: draft.password,
        configFingerprint: testResult.configFingerprint,
        expectedRevision: account?.revision ?? 0,
        enabled: true,
      })
      setSettings(next)
      passwordRef.current = ''
      setDraft((d) => ({ ...d, password: '' }))
      setTestResult(null)
      setTestedFingerprint(null)
      setStep(5)
    } catch (e) {
      setSaveError(errText(e) || '邮箱尚未保存')
    } finally {
      setSaving(false)
    }
  }

  async function runSendSelf() {
    if (sending || !settings?.account) return
    setSending(true)
    setSendError('')
    setSendMessage('')
    try {
      const res = await sendMailTestToSelf({
        expectedRevision: settings.account.revision,
        clientRequestId: `mail-self-${clientReq}`,
      })
      setSendMessage(res.message)
      setShortId(res.shortId || '')
      const refreshed = await getMailSettings()
      setSettings(refreshed)
      if (res.status === 'unknown') {
        setSendError(res.message)
      }
    } catch (e) {
      setSendError(errText(e) || '发送失败')
    } finally {
      setSending(false)
    }
  }

  async function runConfirmReceipt() {
    if (!settings?.account || !shortId) return
    try {
      const next = await confirmMailTestReceipt({
        expectedRevision: settings.account.revision,
        shortId,
      })
      setSettings(next)
      // 切回已完成摘要卡（重新配置 / 更新安全密码）。
      setGuiding(false)
    } catch (e) {
      setSendError(errText(e) || '确认失败')
    }
  }

  if (loadError) {
    return (
      <Section title="邮箱">
        <InlineError>{loadError}</InlineError>
      </Section>
    )
  }

  if (!guiding && account?.onboardingStatus === MailOnboardingStatuses.ReceiptConfirmed) {
    return (
      <Section title="邮箱" hint="账号与登录">
        <ConfiguredSummary account={account} onRestart={() => { setGuiding(true); setStep(1) }} />
      </Section>
    )
  }

  if (!guiding && !account) {
    return (
      <Section title="邮箱" hint="账号与登录">
        <p className="text-[13px] text-muted">接入本人阿里企业邮箱后，可在对话中起草并发送邮件。</p>
        <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={() => { setGuiding(true); setStep(1) }}>开始接入</button>
      </Section>
    )
  }

  return (
    <Section title="邮箱" hint={`接入我的阿里邮箱 · 当前第${step}步 / 共5步`}>
      <ol className="flex flex-wrap gap-2 text-[12px] text-muted m-0 p-0 list-none">
        {STEP_TITLES.map((title, i) => (
          <li key={title} className={i + 1 === step ? 'text-primaryink font-medium' : ''}>
            {i + 1}. {title}
          </li>
        ))}
      </ol>

      {step === 1 && (
        <div className="flex flex-col gap-3">
          <p className="text-[13px] text-ink">你需要一个已经能登录的阿里企业邮箱。这里不是申请新邮箱，也不是使用阿里云控制台账号。</p>
          <label className={LABEL_CLS}>邮箱地址
            <input className={FIELD_CLS} value={draft.address} autoComplete="off" onChange={(e) => patchDraft({ address: e.target.value })} />
          </label>
          <label className={LABEL_CLS}>邮箱版本
            <SelectField value={draft.version} onChange={(v) => patchDraft({ version: v as MailVersion })}>
              <option value={MailVersions.Standard}>标准版（默认）</option>
              <option value={MailVersions.XC}>国产化版</option>
              <option value={MailVersions.HK}>香港优化</option>
            </SelectField>
          </label>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={BTN} onClick={() => void openMailOfficialURL({ page: mailWebmailPage(draft.version) })}>打开阿里邮箱网页端</button>
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} disabled={!canAdvanceFromStep1(draft.address)} onClick={() => setStep(2)}>我能登录该邮箱，下一步</button>
          </div>
          <p className="text-[12px] text-muted">此按钮只确认网页登录，尚未验证 SMTP。</p>
        </div>
      )}

      {step === 2 && (
        <div className="flex flex-col gap-3">
          <p className="text-[13px] text-ink">接下来生成的是第三方客户端专用密码，不是网页登录密码。智开不会代收手机验证码。</p>
          <ol className="flex flex-col gap-2 m-0 p-0 list-none">
            {PASSWORD_CHECKLIST.map((line, i) => (
              <li key={line} className="flex items-start gap-2 text-[13px] text-ink">
                <span className="flex-none w-5 text-muted tabular-nums">{i + 1}.</span>
                <span>{line}</span>
              </li>
            ))}
          </ol>
          <button type="button" className={BTN} onClick={() => void openMailOfficialURL({ page: 'help_security_password' })}>打开官方图文帮助</button>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={BTN} onClick={() => setStep(1)}>上一步</button>
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} onClick={() => setStep(3)}>我已生成并复制，填写连接信息</button>
          </div>
        </div>
      )}

      {step === 3 && (
        <div className="flex flex-col gap-3">
          <label className={LABEL_CLS}>邮箱地址
            <input className={FIELD_CLS} value={draft.address} autoComplete="off" onChange={(e) => patchDraft({ address: e.target.value })} />
          </label>
          <label className={LABEL_CLS}>显示名称
            <input className={FIELD_CLS} value={draft.displayName} autoComplete="off" onChange={(e) => patchDraft({ displayName: e.target.value })} />
          </label>
          <label className={LABEL_CLS}>专用安全密码
            <div className="flex gap-2">
              <input
                className={`${FIELD_CLS} flex-1`}
                type={showPassword ? 'text' : 'password'}
                value={draft.password}
                autoComplete="new-password"
                onChange={(e) => patchDraft({ password: e.target.value })}
              />
              <button type="button" className={BTN} onMouseDown={() => setShowPassword(true)} onMouseUp={() => setShowPassword(false)} onMouseLeave={() => setShowPassword(false)}>按住显示</button>
            </div>
          </label>
          <p className="text-[12px] text-muted">服务器 {mailHostForVersion(draft.version)} · 端口 465 / SSL-TLS（固定）</p>
          {testResult && (
            testResult.ok && testValid
              ? <Banner tone="neutral" title="连接测试通过">{testResult.message}</Banner>
              : <InlineError>{testResult.message || '连接测试未通过或已失效'}</InlineError>
          )}
          <div className="flex flex-wrap gap-2">
            <button type="button" className={BTN} onClick={() => setStep(2)}>上一步</button>
            <button type="button" className={BTN} disabled={testing || !draft.password} onClick={() => void runTest()}>
              {testing ? '正在验证账号…' : '测试连接'}
            </button>
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} disabled={!testValid || testing} onClick={() => setStep(4)}>保存邮箱账号</button>
          </div>
        </div>
      )}

      {step === 4 && (
        <div className="flex flex-col gap-3">
          {!secretsOk && <Banner tone="warning">系统安全存储不可用，无法安全保存邮箱密码。</Banner>}
          {!testValid && <InlineError>连接测试已失效，请返回上一步重新测试。</InlineError>}
          {saveError && <InlineError>{saveError}</InlineError>}
          <p className="text-[13px] text-ink">将把当前已测试通过的配置写入本机受保护存储。前端自称“已测试”不能取代服务端校验。</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={BTN} onClick={() => setStep(3)}>上一步</button>
            <button type="button" className={`${BTN} ${BTN_PRIMARY}`} disabled={!testValid || saving || !secretsOk} onClick={() => void runSave()}>
              {saving ? '正在保存…' : '确认安全保存'}
            </button>
          </div>
        </div>
      )}

      {step === 5 && (
        <div className="flex flex-col gap-3">
          {settings?.account ? (
            <>
              <p className="text-[13px] text-ink">
                我们将从 <strong>{settings.account.address}</strong> 发一封固定测试邮件到同一个地址。不会发给通信录或外部联系人。
              </p>
              {sendMessage && (
                <Banner tone={settings.account.onboardingStatus === MailOnboardingStatuses.TestMailAccepted ? 'neutral' : 'warning'}>
                  {sendMessage}
                </Banner>
              )}
              {sendError && <InlineError>{sendError}</InlineError>}
              {shortId && <p className="text-[12px] text-muted">短编号：{shortId}</p>}
              <div className="flex flex-wrap gap-2">
                <button type="button" className={`${BTN} ${BTN_PRIMARY}`} disabled={sending} onClick={() => void runSendSelf()} data-testid="mail-send-test">
                  {sending ? '正在发送…' : '发送测试邮件'}
                </button>
                <button type="button" className={BTN} onClick={() => void openMailOfficialURL({ page: mailWebmailPage(settings.account!.version) })}>打开邮箱网页</button>
                <button
                  type="button"
                  className={`${BTN} ${BTN_PRIMARY}`}
                  disabled={!shortId}
                  title={!shortId ? '请先发送测试邮件' : undefined}
                  onClick={() => void runConfirmReceipt()}
                  data-testid="mail-confirm-receipt"
                >
                  已收到邮件
                </button>
              </div>
            </>
          ) : (
            <InlineError>尚未保存邮箱账号</InlineError>
          )}
        </div>
      )}
    </Section>
  )
}

function ConfiguredSummary({ account, onRestart }: { account: MailAccountView; onRestart: () => void }) {
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[13px] text-ink">{account.address}{account.displayName ? `（${account.displayName}）` : ''}</p>
      <p className="text-[12px] text-muted">状态：接入与自测完成 · {account.host}:{account.port}</p>
      <button type="button" className={BTN} onClick={onRestart}>重新配置 / 更新安全密码</button>
    </div>
  )
}
