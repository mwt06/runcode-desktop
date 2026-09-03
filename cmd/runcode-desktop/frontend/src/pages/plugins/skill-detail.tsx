// SkillDetail 是技能的全屏详情/编辑页。技能均为用户/项目文件，可编辑。
import { useState } from 'react'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import { deleteSkill, errText, saveSkill, type SkillInfo } from '@/core/bridge'
import { InlineError } from '@/ui/feedback'
import { ConfirmDialog } from '@/ui/confirm-dialog'

export function SkillDetail({ skill, onBack, onChanged, onUse }: {
  skill: SkillInfo | 'new'
  onBack: () => void
  onChanged: () => void
  onUse: (name: string) => void
}) {
  const isNew = skill === 'new'
  const sk = isNew ? null : skill
  const [name, setName] = useState(sk?.name ?? '')
  // 展示名要带进编辑态再原样存回去。保存是整块重写 frontmatter 的,少带这一栏
  // 就等于删掉它——改一次描述,市场给的中文名就没了,而没人会把两件事联系起来。
  const [displayName, setDisplayName] = useState(sk?.displayName ?? '')
  const [displayDescription, setDisplayDescription] = useState(sk?.displayDescription ?? '')
  const [description, setDescription] = useState(sk?.description ?? '')
  const [body, setBody] = useState(sk?.body ?? '')
  const [scope, setScope] = useState(isNew ? 'project' : sk?.source === 'user' ? 'user' : 'project')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // 删除要先问一句：它会把整个技能目录（SKILL.md 连同 references/、scripts/）删掉，
  // 撤不回来。旁边就是「保存」，误点一次的代价太大。
  const [confirmDel, setConfirmDel] = useState(false)
  async function save() {
    setBusy(true); setError('')
    try { await saveSkill({ originalName: sk?.name ?? '', name: name.trim(), displayName, displayDescription, description, body, scope }); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }
  async function remove() {
    if (!sk) return
    setBusy(true); setError('')
    try { await deleteSkill(sk.name, sk.source); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
  }
  return (
    <div className="flex-1 overflow-y-auto px-10 py-7">
      <div className="max-w-[820px] mx-auto flex flex-col gap-4">
        <button type="button" className="self-start text-[13px] text-muted hover:text-ink inline-flex items-center gap-1" onClick={onBack}>← 返回列表</button>
        <label className={LABEL_CLS}>标识(目录名,AI 也用它来称呼这个技能)<input className={FIELD_CLS} value={name} onChange={(e) => setName(e.target.value)} placeholder="如 ppt-maker(字母、数字、- 或 _)" /></label>
        <label className={LABEL_CLS}>显示名(可空,列表里优先显示它)<input className={FIELD_CLS} value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="如 PPT 制作(留空则显示上面的标识)" /></label>
        <label className={LABEL_CLS}>
          范围
          {isNew ? (
            <select className={FIELD_CLS} value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="project">项目(仅本工作区 .runcode/skills)</option>
              <option value="user">用户(全局，所有项目可用)</option>
            </select>
          ) : (
            <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-field px-3 py-2.5">{scope === 'user' ? '用户(全局)' : '项目(本工作区)'}</div>
          )}
        </label>
        <label className={LABEL_CLS}>描述(一句话，告诉 AI 何时加载它)<input className={FIELD_CLS} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="如 制作 PPT 演示文稿" /></label>
        {/* 与上面那句分开：那句是给模型判断何时加载用的(市场包常写成 "Use when…")，
            改它会影响技能什么时候被触发；这句只管列表里怎么读。 */}
        <label className={LABEL_CLS}>显示描述(可空，列表里优先显示它)<input className={FIELD_CLS} value={displayDescription} onChange={(e) => setDisplayDescription(e.target.value)} placeholder="给人读的一句话(留空则显示上面那句)" /></label>
        <label className={LABEL_CLS}>
          正文(Markdown，技能的完整指令)
          <textarea className={`${FIELD_CLS} min-h-[320px] font-mono text-[13px] leading-[1.6] resize-y`} value={body} onChange={(e) => setBody(e.target.value)} />
        </label>
        {error && <InlineError variant="text">{error}</InlineError>}
        <div className="flex gap-2.5">
          <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>
          {!isNew && <button className={BTN} onClick={() => onUse(sk!.name)}>在对话中使用</button>}
          {!isNew && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={() => setConfirmDel(true)}>删除</button>}
        </div>
      </div>
      {confirmDel && sk && (
        <ConfirmDialog
          title="删除技能"
          message={<>确定删除「<b className="text-ink font-semibold">{sk.displayName || sk.name}</b>」？整个技能文件夹（含 references/、scripts/ 等随附文件）会被删掉，此操作不可撤销。</>}
          confirmLabel="删除"
          onConfirm={() => { setConfirmDel(false); void remove() }}
          onCancel={() => setConfirmDel(false)}
        />
      )}
    </div>
  )
}
