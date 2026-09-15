// 出网代理：读写都直接落后端，完全自包含。覆盖三处，都是"直连不通才需要"的境外链路：
//   1. WebFetch 抓网页；
//   2. 未登录通行证时那条 DuckDuckGo 版 WebSearch；
//   3. ChatGPT(Codex) 的登录与请求。
// 前两条共用引擎按 WebProxy 建的客户端，**生效于下个会话**(客户端在建会话时构造)；
// 第三条按请求实时读设置，改完立刻生效。这个差别源于两者客户端的寿命，不是有意的不一致。
//
// 不覆盖的两处：通行证的模型请求与平台搜索(Bridge 常部署在内网，塞进公网代理只会
// 连不上)，以及自定义模型自己的端点(那是用户自己填的地址，多为直连可达)。
import { useEffect, useState } from 'react'
import { BTN } from '@/ui/tokens'
import { FIELD_CLS } from '@/ui/fields'
import { errText, setWebProxy, webProxy } from '@/core/bridge'
import { Section } from './section'

export function ProxySection() {
  const [proxy, setProxy] = useState('')
  const [proxyMsg, setProxyMsg] = useState('')
  useEffect(() => {
    webProxy().then((p) => setProxy(p ?? '')).catch(() => {})
  }, [])
  return (
    <Section title="出网代理" hint="联网工具 / ChatGPT">
      <p className="text-[12px] text-muted -mt-1.5">
        直连不通时在此填代理，影响三处：<b>WebFetch</b> 抓网页、未登录通行证时的 <b>DuckDuckGo 联网搜索</b>、以及 <b>ChatGPT（Codex）的登录与请求</b>。留空为直连。
      </p>
      <p className="text-[12px] text-faint -mt-1">
        不影响通行证的模型请求与平台搜索（走内网网关，无需代理），也不影响自定义模型自己填的端点。
      </p>
      <div className="flex gap-2">
        <input
          className={`${FIELD_CLS} flex-1`}
          placeholder="如 127.0.0.1:7890（可省略 http://，支持 socks5://）"
          value={proxy}
          onChange={(e) => { setProxy(e.target.value); setProxyMsg('') }}
        />
        <button type="button" className={`${BTN} px-5 flex-none`} onClick={async () => {
          setProxyMsg('')
          try {
            const norm = await setWebProxy(proxy)
            setProxy(norm ?? '')
            setProxyMsg(norm ? `已保存：${norm}（ChatGPT 立即生效，联网工具下个会话生效）` : '已清除，将直连')
          } catch (e) {
            setProxyMsg(errText(e))
          }
        }}>保存</button>
      </div>
      {proxyMsg && <div className="text-[12px] text-muted -mt-1">{proxyMsg}</div>}
      <p className="text-[12px] text-faint -mt-1">
        出于安全，联网工具始终拒绝访问内网/回环地址(如 127.0.0.1、192.168.*、169.254.169.254)，配了代理也一样。
      </p>
    </Section>
  )
}
