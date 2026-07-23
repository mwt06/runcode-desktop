// 登录门：未登录且本地没有自定义直连模型时的整屏入口——两种登录方式，外加一个
// 内嵌的自定义模型小节，让没有通行证的用户也能先配一个直连接入点直接开工。
import { passportCancelLogin, type CustomModel } from '@/core/bridge'
import { CustomModelsSection } from '../settings/custom-models'
import { loginBg, loginMascot } from './splash'

export function LoginGate({ loggingIn, error, customModels, onLogin, onCustomModelsChanged }: {
  loggingIn: boolean
  error: string
  customModels: CustomModel[]
  onLogin: (scheme: string) => void
  onCustomModelsChanged: (list: CustomModel[]) => void
}) {
  return (
    <div
      className="relative flex-1 min-h-0 overflow-y-auto bg-cover bg-center px-6 py-10"
      style={{ backgroundImage: `url(${loginBg})` }}
    >
      <div className="mx-auto flex w-full max-w-[640px] flex-col items-center">
        <img src={loginMascot} alt="" draggable={false} className="w-[190px] h-auto select-none pointer-events-none" />
        <h1 className="mt-7 mb-10 text-[26px] font-bold tracking-[0.06em]" style={{ color: '#1d55c4' }}>
          智开AI，您的AI办公助手
        </h1>
        <div className="flex items-stretch gap-4">
          <button
            type="button"
            disabled={loggingIn}
            onClick={() => onLogin('OneOuchnPassport')}
            className="w-[230px] py-3.5 rounded-full text-white text-[16px] font-semibold tracking-[0.12em] shadow-[0_10px_24px_rgba(46,107,255,0.35)] transition-transform hover:scale-[1.02] active:scale-[0.99] disabled:opacity-70 disabled:cursor-default"
            style={{ background: 'linear-gradient(90deg, #2050d8 0%, #3f7bff 55%, #55a5ff 100%)' }}
          >
            {loggingIn ? '等待浏览器登录…' : '统一认证登录'}
          </button>
          <button
            type="button"
            disabled={loggingIn}
            onClick={() => onLogin('')}
            className="w-[230px] py-3.5 rounded-full text-[15px] font-semibold tracking-[0.12em] border-2 bg-white/80 hover:bg-white transition-colors disabled:opacity-70 disabled:cursor-default"
            style={{ color: '#2050d8', borderColor: '#2050d8' }}
          >
            基座通行证登录
          </button>
        </div>
        {loggingIn && (
          <button type="button" className="mt-4 text-[13px] text-muted hover:text-ink" onClick={() => void passportCancelLogin()}>
            取消登录
          </button>
        )}
        {error && <div className="mt-4 max-w-[420px] text-center text-red text-[13px]">{error}</div>}
        <div className="mt-8 w-full">
          <CustomModelsSection models={customModels} onChanged={onCustomModelsChanged} />
        </div>
      </div>
    </div>
  )
}
