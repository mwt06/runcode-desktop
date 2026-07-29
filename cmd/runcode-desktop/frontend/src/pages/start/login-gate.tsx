// 登录门：强制登录的整屏入口——只提供两种登录方式。登录是强制的（除非部署在
// desktop.json 里开了 skipLogin），所以这里不再内嵌自定义模型配置——自定义模型
// 在设置页管理，避免登录页上出现一个配了也无法继续的入口。
import { passportCancelLogin } from '@/core/bridge'
import { BRAND } from '@/core/brand'
import { loginBg, loginMascot } from './splash'

export function LoginGate({ loggingIn, error, onLogin }: {
  loggingIn: boolean
  error: string
  onLogin: (scheme: string) => void
}) {
  return (
    <div
      className="relative flex-1 min-h-0 overflow-y-auto bg-cover bg-center"
      style={{ backgroundImage: `url(${loginBg})` }}
    >
      {/* min-h-full + justify-center vertically centers the login content, yet lets it
          scroll (rather than clip) on a window too short to fit it. */}
      <div className="mx-auto flex min-h-full w-full max-w-[640px] flex-col items-center justify-center px-6 py-10">
        <img src={loginMascot} alt="" draggable={false} className="w-[190px] h-auto select-none pointer-events-none" />
        <h1 className="mt-7 mb-10 text-[26px] font-bold tracking-[0.06em]" style={{ color: '#1d55c4' }}>
          {BRAND.loginHeadline}
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
      </div>
    </div>
  )
}
