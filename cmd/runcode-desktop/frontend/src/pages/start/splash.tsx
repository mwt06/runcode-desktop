import { type ReactNode } from 'react'
import loginBg from '@/assets/login-bg.jpg'
import loginMascot from '@/assets/login-mascot.svg'

// Splash 是起始页的过渡屏底座：登录背景 + 吉祥物居中，下方放一句状态文案。
// 启动校验中与自动进入工作区两态共用，保证过渡之间不闪背景。
export function Splash({ children }: { children: ReactNode }) {
  return (
    <div
      className="relative flex flex-col items-center justify-center flex-1 min-h-0 bg-cover bg-center"
      style={{ backgroundImage: `url(${loginBg})` }}
    >
      <img src={loginMascot} alt="" draggable={false} className="w-[150px] h-auto select-none pointer-events-none opacity-90" />
      {children}
    </div>
  )
}

// SplashSpinner：浅背景上白色转圈不可见，改用品牌蓝——淡蓝底轨 + 蓝色渐变彗尾。
// 彗尾用 conic-gradient 填满圆再用 radial mask 抠成 3px 圆环，兼容 WebView2。
export function SplashSpinner() {
  return (
    <div className="mt-8 relative w-11 h-11">
      <div className="absolute inset-0 rounded-full border-[3px]" style={{ borderColor: 'rgba(32,80,216,0.14)' }} />
      <div
        className="absolute inset-0 rounded-full animate-spin"
        style={{
          background:
            'conic-gradient(from 0deg, rgba(32,80,216,0) 0deg, rgba(63,123,255,0.4) 150deg, #3f7bff 300deg, #2050d8 360deg)',
          WebkitMaskImage: 'radial-gradient(farthest-side, transparent calc(100% - 3px), #000 calc(100% - 3px))',
          maskImage: 'radial-gradient(farthest-side, transparent calc(100% - 3px), #000 calc(100% - 3px))',
        }}
      />
    </div>
  )
}

export { loginBg, loginMascot }
