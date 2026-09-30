import { useEffect, useRef, useState, type RefObject } from 'react'
import { Events, errText, installMarketSkill, listSkills, onEvent, skillMarket } from '@/core/bridge'
import type { Scenario } from '@/core/scenarios'
import type { InstallState } from '@/composer/install-overlay'
import { pickScenarioAction } from './scenario-actions'
import { useActionScope } from './use-action-scope'

export function useScenarioActions({ sessionId, input, setInput, textarea, notify }: {
  sessionId: string
  input: string
  setInput: (value: string) => void
  textarea: RefObject<HTMLTextAreaElement>
  notify: (message: string) => void
}) {
  const [installing, setInstalling] = useState<InstallState | null>(null)
  const scope = useActionScope(sessionId)
  const latest = useRef(input)
  latest.current = input
  const picking = useRef(false)
  // 进度订阅常驻，不能等下载开始才挂。
  useEffect(() => onEvent(Events.SkillInstall, (p) => {
    setInstalling((cur) => cur ? { ...cur, progress: p } : cur)
  }), [])

  async function pickScenario(sc: Scenario) {
    if (picking.current) return
    picking.current = true
    const current = scope.capture()
    try {
      await pickScenarioAction(sc, {
        list: listSkills,
        market: () => skillMarket(false),
        install: (id) => installMarketSkill(id, 'user'),
        installing: (name) => setInstalling(name ? { name, progress: null } : null),
        notify,
        errorText: errText,
      }, {
        current,
        read: () => latest.current,
        apply: ({ value, start, end }) => {
          setInput(value)
          requestAnimationFrame(() => {
            if (!current()) return
            textarea.current?.focus()
            textarea.current?.setSelectionRange(start, end)
          })
        },
      })
    } finally { picking.current = false }
  }
  return { installing, pickScenario }
}
