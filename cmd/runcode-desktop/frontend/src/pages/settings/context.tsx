// 上下文长度控制：三个字段都进 saveSettings() 载荷，受控于父级。
import { FIELD_CLS, LABEL_CLS, SelectField } from '@/ui/fields'
import { Section } from './section'

export function ContextSection({ maxTokens, onMaxTokens, maxContextTokens, onMaxContextTokens, maxHistoryMessages, onMaxHistoryMessages }: {
  maxTokens: string
  onMaxTokens: (v: string) => void
  maxContextTokens: number
  onMaxContextTokens: (v: number) => void
  maxHistoryMessages: string
  onMaxHistoryMessages: (v: string) => void
}) {
  return (
    <Section title="上下文长度控制" hint="下次新建会话生效">
      <label className={LABEL_CLS}>最大输出 Tokens<input className={FIELD_CLS} type="number" value={maxTokens} onChange={(e) => onMaxTokens(e.target.value)} placeholder="留空则用默认 16384" /></label>
      <label className={LABEL_CLS}>上下文预算（超出后自动总结压缩较早对话；磁盘记录保持完整）
        <SelectField value={String(maxContextTokens)} onChange={(v) => onMaxContextTokens(parseInt(v, 10))}>
          <option value="0">关闭 · 不自动压缩</option>
          <option value="32000">32K · 省 token</option>
          <option value="128000">128K · 推荐</option>
          <option value="200000">200K · 大窗口</option>
        </SelectField>
      </label>
      <label className={LABEL_CLS}>历史消息上限（硬截断，仅保留最近 N 条；留空关闭）
        <input className={FIELD_CLS} type="number" value={maxHistoryMessages} onChange={(e) => onMaxHistoryMessages(e.target.value)} placeholder="留空 = 不截断（推荐优先用上面的自动压缩）" />
      </label>
    </Section>
  )
}
