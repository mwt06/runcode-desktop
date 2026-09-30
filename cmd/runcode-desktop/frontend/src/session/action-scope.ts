// 异步界面动作属于发起时的会话；切走再切回来也不复活旧动作。
export function createActionScope() {
  let id = ''
  let generation = 0
  let active = true
  return {
    focus(next: string) {
      if (next !== id) { id = next; generation++ }
    },
    activate() { active = true },
    deactivate() { active = false },
    invalidate() { generation++ },
    capture() {
      const gen = generation
      const owner = id
      return () => active && !!owner && generation === gen
    },
  }
}
