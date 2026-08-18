// 联网工具(WebSearch/WebFetch)代理：与模型出口无关，读写都直接落后端，完全自包含。
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
    <Section title="联网工具代理" hint="仅 WebSearch / WebFetch">
      <p className="text-[12px] text-muted -mt-1.5">
        联网搜索走 DuckDuckGo，直连不通时可在此填代理。<b>只影响联网工具</b>，不改变模型 API 与通行证的出口。留空为直连。
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
            setProxyMsg(norm ? `已保存：${norm}（新建会话后生效）` : '已清除，联网工具将直连')
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
