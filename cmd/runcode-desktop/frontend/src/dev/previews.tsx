// 样式预览页：用假数据渲染各类卡片，方便不开真实会话就核对视觉。
// 由 main.tsx 按 ?preview=tools / ?preview=thinking 挂载，正常流程不会用到。
import { type ToolEvent } from '@/core/bridge'
import { type Block } from '@/chat/blocks'
import { AnalyzeCard } from '@/chat/analyze-card'
import { BlockView } from '@/chat/block-view'
import { ContextMeter } from '@/chat/context-meter'
import { ExecutionCard } from '@/chat/execution-card'
import { ToolDetail } from '@/chat/tool-detail'

// ToolPreview renders the tool cards with mock data so the styling can be reviewed
// without a live session (mounted via ?preview=tools).
export function ToolPreview() {
  const bash: ToolEvent = {
    type: 'completed', toolName: 'Bash', toolUseID: 'b1',
    input: { command: 'go test ./internal/repl/ -run TestStream -count=1', timeout: 120000 },
    output: [
      { stream: 'stdout', text: 'ok  \tgithub.com/wt68/runcode/internal/repl\t2.81s' },
      { stream: 'info', text: '退出码 0 · 2.83s' },
    ],
  }
  const edit: ToolEvent = {
    type: 'completed', toolName: 'Edit', toolUseID: 'e1',
    input: { path: 'internal/repl/session.go' },
    files: [{ path: 'internal/repl/session.go', kind: 'read' }],
    output: [
      { stream: 'diff_context', text: '  func (s *Session) buildRequest() {' },
      { stream: 'diff_context', text: '    promptOpts.Tools = tools' },
      { stream: 'diff_del', text: '-   promptOpts.Skills = s.skills' },
      { stream: 'diff_add', text: '+   promptOpts.Skills = s.currentSkillsCatalog()' },
      { stream: 'diff_add', text: '+   promptOpts.Agents = s.currentAgentsCatalog()' },
      { stream: 'diff_context', text: '    system, _ := prompt.Build(promptOpts)' },
    ],
  }
  const grep: ToolEvent = {
    type: 'completed', toolName: 'Grep', toolUseID: 'g1',
    input: { pattern: 'StreamDelta', path: 'internal', glob: '*.go', output_mode: 'files_with_matches' },
    // 50 entries on purpose: enough to overflow the list's max-height (a flex-squash
    // bug once hid exactly this), mixing root files + two directories so the
    // MatchedFileTree rendering (collapsed dir chains, indentation) is exercised too.
    files: [
      { path: 'index.html', kind: 'matched' as const },
      { path: 'README.md', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/image_analysis.csv', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/slides_outline.md', kind: 'matched' as const },
      { path: 'projects/matrix_transformations/analysis/notes.txt', kind: 'matched' as const },
      ...Array.from({ length: 45 }, (_, i) => ({
        path: `projects/matrix_transformations/svg_output/${String(i + 1).padStart(2, '0')}_矩阵变换_较长文件名示例.svg`,
        kind: 'matched' as const,
      })),
    ],
    filesTotal: 76,
  }
  const running: ToolEvent = {
    type: 'progress', toolName: 'Bash', toolUseID: 'r1',
    input: { command: 'npm run build' },
    output: [
      { stream: 'stdout', text: 'vite v6.4.3 building for production...' },
      { stream: 'stdout', text: '✓ 812 modules transformed' },
    ],
  }
  const readImg: ToolEvent = {
    type: 'completed', toolName: 'Read', toolUseID: 'r2',
    input: { path: 'docs/design.png' }, files: [{ path: 'docs/design.png', kind: 'read' }],
    output: [{ stream: 'stdout', text: '[image: design.png]' }],
    image: { media_type: 'image/svg+xml', url: 'data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzNjAiIGhlaWdodD0iMjAwIj48cmVjdCB3aWR0aD0iMzYwIiBoZWlnaHQ9IjIwMCIgZmlsbD0iIzBGNzY2RSIvPjxyZWN0IHg9IjI0IiB5PSIyNiIgd2lkdGg9IjExMCIgaGVpZ2h0PSIxMiIgcng9IjQiIGZpbGw9IiNGNTlFMEIiLz48dGV4dCB4PSIyNCIgeT0iMTE1IiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIyNCIgZmlsbD0iI2ZmZmZmZiI+ZGVzaWduLnBuZzwvdGV4dD48dGV4dCB4PSIyNCIgeT0iMTUwIiBmb250LWZhbWlseT0ic2Fucy1zZXJpZiIgZm9udC1zaXplPSIxNSIgZmlsbD0iIzk5RjZFNCI+dGh1bWJuYWlsIHByZXZpZXc8L3RleHQ+PC9zdmc+' },
  }
  return (
    <div className="min-h-screen p-8">
      <div className="max-w-[940px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">工具卡样式预览</h2>
        <div className="text-[13px] text-muted">折叠态紧凑列表(每行:图标 + 动词 + 目标 + 状态),点行展开详情;运行中的自动展开:</div>
        <ExecutionCard tools={[bash, edit, grep, readImg]} />
        <ExecutionCard tools={[running]} />
        <div className="text-[13px] text-muted">图片 Read 展开(缩略图):</div>
        <div className="bg-surface border border-line2 rounded-card p-4"><ToolDetail tool={readImg} /></div>
        <div className="text-[13px] text-muted">查找/搜索展开(匹配文件列表):</div>
        <div className="bg-surface border border-line2 rounded-card p-4"><ToolDetail tool={grep} /></div>
      </div>
    </div>
  )
}

// ThinkingPreview renders assistant blocks carrying reasoning ("thinking") in each
// state, plus the context meter and analyze card, so their rendering can be verified
// without a live model (mounted via ?preview=thinking).
export function ThinkingPreview() {
  const reason =
    '用户问 9.11 和 9.9 哪个大。先把它们对齐小数位:9.11 = 9.110,9.9 = 9.900。' +
    '比较小数部分 0.110 与 0.900,显然 0.900 更大。所以 9.9 > 9.11。'
  const blocks: Block[] = [
    { kind: 'assistant', id: 't1', text: '', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't2', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: true, ts: '' },
    { kind: 'assistant', id: 't3', text: '**9.9 更大**。对齐小数位后 9.900 > 9.110。', thinking: reason, streaming: false, ts: '11:42' },
  ]
  const labels = ['① 仅思考中(自动展开)', '② 思考完 + 答案流式(自动折叠)', '③ 完成态(可点开)']
  const noop = () => {}
  return (
    <div className="min-h-screen p-8">
      <div className="w-full max-w-[1280px] mx-auto flex flex-col gap-6">
        <h2 className="text-[18px] font-bold tracking-tight">上下文用量条(不同占用)</h2>
        <div className="flex flex-col gap-3 bg-surface border border-line2 rounded-xl p-4">
          {[10000, 96000, 120000, 130000].map((u) => (
            <div key={u} className="flex items-center gap-4 text-[13px] text-muted">
              <ContextMeter used={u} budget={128000} onCompact={noop} compacting={false} busy={false} />
            </div>
          ))}
          <div className="flex items-center gap-4 text-[13px] text-muted">
            <ContextMeter used={44000} budget={0} onCompact={noop} compacting={false} busy={false} />
          </div>
          <div className="flex items-center gap-4 text-[13px] text-muted">
            <ContextMeter used={18000} budget={200000} estimated onCompact={noop} compacting={false} busy={false} />
          </div>
        </div>
        <h2 className="text-[18px] font-bold tracking-tight">结构化思考卡片</h2>
        <AnalyzeCard
          tool={{
            type: 'completed',
            toolName: 'Analyze',
            toolUseID: 'a1',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 token。' },
                { key: 'validation', label: '验证方式', content: '抓包对比 401 前后的 token;核对服务端/客户端时间;灰度关闭网关缓存。' },
                { key: 'root_cause', label: '最可能根因', content: '并发刷新时旧 token 覆盖了新 token(竞态)。' },
                { key: 'fix', label: '修复方案', content: '刷新加互斥锁 + 单飞(single-flight);失败仅重试一次。' },
                { key: 'verification', label: '验证方法', content: '压测并发刷新场景;监控 401 率回落到 0 且无回归。' },
              ],
            },
          }}
        />
        <div className="text-[13px] text-muted">流式中(部分步骤已填、后续为空 + 分析中…):</div>
        <AnalyzeCard
          tool={{
            type: 'progress',
            toolName: 'Analyze',
            toolUseID: 'a2',
            input: {
              method: '5 Whys + 假设验证 + 奥卡姆剃刀',
              steps: [
                { key: 'symptom', label: '现象与范围', content: '登录后偶发 401,约 5% 请求,集中在移动端弱网环境。' },
                { key: 'hypotheses', label: '可能假设', content: '1) token 刷新竞态;2) 客户端时钟偏移导致过期误判;3) 网关缓存了旧 to' },
                { key: 'validation', label: '验证方式', content: '' },
                { key: 'root_cause', label: '最可能根因', content: '' },
                { key: 'fix', label: '修复方案', content: '' },
                { key: 'verification', label: '验证方法', content: '' },
              ],
            },
          }}
        />
        <h2 className="text-[18px] font-bold tracking-tight">思考面板样式预览</h2>
        {blocks.map((b, i) => (
          <div key={b.id} className="flex flex-col gap-2">
            <div className="text-[13px] text-muted">{labels[i]}</div>
            <BlockView block={b} />
          </div>
        ))}
      </div>
    </div>
  )
}
