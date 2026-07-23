// SkillDetail 是技能的全屏详情/编辑页。技能均为用户/项目文件，可编辑。
import { useState } from 'react'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { FIELD_CLS, LABEL_CLS } from '@/ui/fields'
import { deleteSkill, errText, saveSkill, type SkillInfo } from '@/core/bridge'

export function SkillDetail({ skill, onBack, onChanged, onUse }: {
  skill: SkillInfo | 'new'
  onBack: () => void
  onChanged: () => void
  onUse: (name: string) => void
}) {
  const isNew = skill === 'new'
  const sk = isNew ? null : skill
  const [name, setName] = useState(sk?.name ?? '')
  const [description, setDescription] = useState(sk?.description ?? '')
  const [body, setBody] = useState(sk?.body ?? '')
  const [scope, setScope] = useState(isNew ? 'project' : sk?.source === 'user' ? 'user' : 'project')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function save() {
    setBusy(true); setError('')
    try { await saveSkill({ originalName: sk?.name ?? '', name: name.trim(), description, body, scope }); onChanged(); onBack() } catch (e) { setError(errText(e)) } finally { setBusy(false) }
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
        <label className={LABEL_CLS}>名称<input className={FIELD_CLS} value={name} onChange={(e) => setName(e.target.value)} placeholder="如 ppt-maker(字母、数字、- 或 _)" /></label>
        <label className={LABEL_CLS}>
          范围
          {isNew ? (
            <select className={FIELD_CLS} value={scope} onChange={(e) => setScope(e.target.value)}>
              <option value="project">项目(仅本工作区 .runcode/skills)</option>
              <option value="user">用户(全局，所有项目可用)</option>
            </select>
          ) : (
            <div className="text-[13px] text-ink bg-surface2 border border-line2 rounded-[9px] px-3 py-2.5">{scope === 'user' ? '用户(全局)' : '项目(本工作区)'}</div>
          )}
        </label>
        <label className={LABEL_CLS}>描述(一句话，告诉 AI 何时加载它)<input className={FIELD_CLS} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="如 制作 PPT 演示文稿" /></label>
        <label className={LABEL_CLS}>
          正文(Markdown，技能的完整指令)
          <textarea className={`${FIELD_CLS} min-h-[320px] font-mono text-[13px] leading-[1.6] resize-y`} value={body} onChange={(e) => setBody(e.target.value)} />
        </label>
        {error && <div className="text-[12.5px] text-red">{error}</div>}
        <div className="flex gap-2.5">
          <button className={`${BTN} ${BTN_PRIMARY}`} disabled={busy || !name.trim()} onClick={save}>{busy ? '保存中…' : '保存'}</button>
          {!isNew && <button className={BTN} onClick={() => onUse(sk!.name)}>在对话中使用</button>}
          {!isNew && <button className={`${BTN} ${BTN_DANGER}`} disabled={busy} onClick={remove}>删除</button>}
        </div>
      </div>
    </div>
  )
}
