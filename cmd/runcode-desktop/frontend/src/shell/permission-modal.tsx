// 权限请求弹窗：把一次工具调用的动作、目标与风险摊开给用户判断，四个选项对应
// 不同的记忆范围。并发请求排队时，右上角显示还剩几个，并给一个"全部拒绝"的出口。
import { Icon } from '@/ui/icons'
import { BTN, BTN_PRIMARY, BTN_DANGER } from '@/ui/tokens'
import { type PermissionRequest } from '@/core/bridge'

export function PermissionModal({ req, onDecide, remaining = 0, onDenyRest }: { req: PermissionRequest; onDecide: (decision: string) => void; remaining?: number; onDenyRest?: () => void }) {
  const s = req.summary
  const td = 'py-[7px] px-1.5 align-top border-t border-line'
  return (
    <div className="fixed inset-0 bg-[rgba(30,33,50,0.32)] backdrop-blur-[2px] flex items-center justify-center z-20 anim-rise">
      <div className="w-[560px] max-w-[92vw] bg-surface rounded-[16px] p-[22px] shadow-[0_30px_70px_rgba(30,35,60,0.28)]">
        <h3 className="m-0 mb-4 text-[16px] font-bold flex items-center gap-2.5">
          <span className="w-[9px] h-[9px] rounded-[3px] bg-primary" />权限请求
          {remaining > 0 && (
            <span className="ml-auto text-[12px] font-medium text-muted bg-surface2 border border-line2 rounded-full px-2.5 py-0.5">还有 {remaining} 个待处理</span>
          )}
        </h3>
        {req.samplingServer ? (
          <div className="mb-3.5">
            <div className="flex items-start gap-2 bg-primarysoft border border-line2 rounded-lg px-3 py-2.5">
              <span className="text-primaryink flex-none mt-px"><Icon name="bot" size={16} /></span>
              <div className="min-w-0 text-[13px] text-ink">
                MCP 服务器 <b className="font-mono">{req.samplingServer}</b> 请求使用你的模型生成一段内容（sampling）。
                <div className="text-[12px] text-muted mt-1">允许即用你配置的模型和额度替它完成一次生成。选「本次会话」后本会话内不再询问；仅在信任该服务器时允许。</div>
              </div>
            </div>
          </div>
        ) : (
          <>
            {req.harmReason && (
              <div className="mb-3 flex items-start gap-2 bg-redbg border border-[rgba(224,86,74,0.35)] rounded-lg px-3 py-2.5">
                <span className="text-red flex-none mt-px"><Icon name="shield" size={16} /></span>
                <div className="min-w-0">
                  <div className="text-[12.5px] font-semibold text-red">模型判定可能有害</div>
                  <div className="text-[12.5px] text-ink mt-0.5 break-words">{req.harmReason}</div>
                </div>
              </div>
            )}
            {req.command && (
              <div className="mb-3">
                <div className="text-[12px] text-muted mb-1.5">将执行命令</div>
                <pre className="m-0 bg-surface2 border border-line rounded-lg px-3 py-2.5 font-mono text-[12.5px] text-ink whitespace-pre-wrap break-all max-h-[160px] overflow-auto">{req.command}</pre>
              </div>
            )}
            <table className="w-full border-collapse text-[13px]">
              <tbody>
                <tr><td className={`${td} text-muted w-16`}>工具</td><td className={td}>{s.toolName}</td></tr>
                <tr><td className={`${td} text-muted`}>操作</td><td className={td}>{s.operation} · 风险 {s.risk}{s.commandCategory ? ` · ${s.commandCategory}` : ''}</td></tr>
                {s.networkHost && <tr><td className={`${td} text-muted`}>主机</td><td className={td}>{s.networkHost}</td></tr>}
                {s.mcpServer && <tr><td className={`${td} text-muted`}>MCP</td><td className={td}>{s.mcpServer}/{s.mcpTool}</td></tr>}
                {req.targets && req.targets.length > 0 && (
                  <tr><td className={`${td} text-muted`}>目标</td><td className={td}>{req.targets.map((t) => <code key={t} className="bg-inset px-1.5 py-0.5 rounded mr-1.5 mb-1.5 inline-block">{t}</code>)}</td></tr>
                )}
              </tbody>
            </table>
            <div className="mt-3.5 text-[12px] text-faint">
              「本次会话」后，对本项目文件的增删改、或同类命令在本次会话内都不再询问（推荐）；「仅此一次」每次都会再问。
            </div>
          </>
        )}
        <div className="flex gap-2.5 mt-2.5">
          <button className={`${BTN} flex-1 ${BTN_PRIMARY}`} onClick={() => onDecide('allow-session')}>本次会话</button>
          <button className={`${BTN} flex-1`} onClick={() => onDecide('allow-once')}>仅此一次</button>
          <button className={`${BTN} flex-1`} onClick={() => onDecide('allow-project')}>本项目</button>
          <button className={`${BTN} flex-1 ${BTN_DANGER}`} onClick={() => onDecide('deny')}>拒绝</button>
        </div>
        {remaining > 0 && onDenyRest && (
          <button className="mt-2 w-full text-[12.5px] text-muted hover:text-red transition-colors" onClick={onDenyRest}>拒绝全部（含其余 {remaining} 个）</button>
        )}
      </div>
    </div>
  )
}
