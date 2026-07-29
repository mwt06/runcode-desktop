// 会话：模型 / 权限模式 / 判定模型 / 判定表决。
// 模型字段是「就地切换」——选中即刻切换当前会话的连接(平台↔本地)并保留对话历史,
// 走的是与输入框内模型选择器同一条 SwitchModel 路径,不需要新建会话;其余字段仍随
// 「保存设置」一起落盘。判定模型只列平台模型:它是交给引擎的模型 id,自定义档名引擎
// 认不出。
import { Icon } from '@/ui/icons'
import { LABEL_CLS, SelectField } from '@/ui/fields'
import { ModelSelect, type ModelOption } from '@/ui/model-picker'
import { Section } from './section'

export function SessionSection({ currentModel, switchOpts, onSwitchModel, busy, permissionMode, onPermissionMode, harmJudgeModel, onHarmJudgeModel, harmJudgeVotes, onHarmJudgeVotes, modelOpts }: {
  currentModel: string
  switchOpts: ModelOption[]
  onSwitchModel: (choice: ModelOption) => void
  busy: boolean
  permissionMode: string
  onPermissionMode: (v: string) => void
  harmJudgeModel: string
  onHarmJudgeModel: (v: string) => void
  harmJudgeVotes: number
  onHarmJudgeVotes: (v: number) => void
  modelOpts: ModelOption[]
}) {
  return (
    <Section title="会话">
      <div className={LABEL_CLS}>模型（切换即刻生效，保留当前对话，无需新建会话）
        <ModelSelect
          value={currentModel}
          options={switchOpts}
          onPick={(_, option) => { if (option) onSwitchModel(option) }}
          placeholder="选择平台或本地模型…"
          disabled={busy}
        />
        {busy && <span className="text-[12px] text-muted">对话进行中，暂不能切换模型。</span>}
      </div>
      <label className={LABEL_CLS}>权限模式
        <SelectField value={permissionMode} onChange={onPermissionMode}>
          <option value="interactive">交互（逐项询问）</option>
          <option value="judge">智能（模型审查命令）</option>
          <option value="safe">安全（拒绝高危）</option>
          <option value="flight">飞行（不审计，全部放行）</option>
        </SelectField>
      </label>
      {permissionMode === 'flight' && (
        <div className="flex items-start gap-2 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-lg px-3 py-2.5 text-[12.5px] text-red">
          <span className="flex-none mt-px"><Icon name="shield" size={15} /></span>
          <span>飞行模式会<b>放行一切操作</b>（含删除文件、sudo 等高危命令），不再询问也不做模型审查。仅在完全信任的环境使用。</span>
        </div>
      )}
      <div className={LABEL_CLS}>判定模型（智能模式的安全判定；留空 = 独立默认，与主模型解耦）
        <ModelSelect value={harmJudgeModel} options={modelOpts} onPick={onHarmJudgeModel} placeholder="留空 = 默认独立模型（如 claude-haiku-4-5）" allowCustom clearLabel="留空 = 默认独立模型" />
      </div>
      <label className={LABEL_CLS}>判定表决（多次独立判定取多数，更稳但更费 token）
        <SelectField value={String(harmJudgeVotes)} onChange={(v) => onHarmJudgeVotes(parseInt(v, 10))}>
          <option value="1">单次（默认）</option>
          <option value="3">3 次取多数</option>
          <option value="5">5 次取多数</option>
        </SelectField>
      </label>
    </Section>
  )
}
