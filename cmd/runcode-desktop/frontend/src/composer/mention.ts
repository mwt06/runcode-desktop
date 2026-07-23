// 输入框里三种「触发器」的纯逻辑：识别光标处是否处于一个触发中，以及各自的候选
// 匹配/排序。与 React 无关，可单测。
export type MentionTrigger = '@' | '/' | '#'

// 一次候选列表最多显示多少条(超出的靠继续输入收窄)。
export const MENTION_LIMIT = 50

// computeMention finds an active mention/command trigger ending at the cursor:
//   '@' (a skill command) and '/' (a sub-agent command) only at the very start;
//   '#' (a file mention) at the start or after whitespace, anywhere in the input.
// In every case there must be no whitespace between the trigger and the cursor.
export function computeMention(value: string, cursor: number): { query: string; start: number; trigger: MentionTrigger } | null {
  const upToCursor = value.slice(0, cursor)
  if (value.startsWith('@') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '@' }
  }
  if (value.startsWith('/') && !/\s/.test(upToCursor.slice(1))) {
    return { query: upToCursor.slice(1), start: 0, trigger: '/' }
  }
  const hash = upToCursor.lastIndexOf('#')
  if (hash < 0) return null
  if (hash > 0 && !/\s/.test(value[hash - 1])) return null
  const query = upToCursor.slice(hash + 1)
  if (/\s/.test(query)) return null
  return { query, start: hash, trigger: '#' }
}

// rankFileMatches ranks the '#' candidates: substring hits anywhere in the path,
// ordered by basename-prefix match first, then by shorter path (a shallower file is
// usually the one meant). An empty query lists the workspace as-is.
export function rankFileMatches(files: string[], query: string, limit = MENTION_LIMIT): string[] {
  const q = query.toLowerCase()
  const hits = q ? files.filter((p) => p.toLowerCase().includes(q)) : files
  return [...hits]
    .sort((a, b) => {
      const ba = a.slice(a.lastIndexOf('/') + 1).toLowerCase()
      const bb = b.slice(b.lastIndexOf('/') + 1).toLowerCase()
      const sa = q && ba.startsWith(q) ? 0 : 1
      const sb = q && bb.startsWith(q) ? 0 : 1
      return sa - sb || a.length - b.length
    })
    .slice(0, limit)
}

// matchByNameOrDesc filters skills ('@') / sub-agents ('/') by name or description.
export function matchByNameOrDesc<T extends { name: string; description: string }>(items: T[], query: string, limit = MENTION_LIMIT): T[] {
  const q = query.toLowerCase()
  return items
    .filter((it) => !q || it.name.toLowerCase().includes(q) || it.description.toLowerCase().includes(q))
    .slice(0, limit)
}

// applyMention splices the picked text over the trigger + its query. '#' keeps the
// rest of the line (a file reference sits inline); '@' and '/' are start-of-input
// commands, so their instruction replaces everything before the cursor. Returns the
// new input and where to put the caret.
export function applyMention(
  input: string,
  mention: { start: number; query: string; trigger: MentionTrigger },
  picked: string,
): { value: string; caret: number } {
  const after = input.slice(mention.start + 1 + mention.query.length)
  if (mention.trigger === '#') {
    const before = input.slice(0, mention.start)
    const insert = '#' + picked + ' '
    return { value: before + insert + after, caret: (before + insert).length }
  }
  const insert = mention.trigger === '@' ? `请使用「${picked}」技能完成：` : `请委派「${picked}」子代理完成：`
  return { value: insert + after, caret: insert.length }
}
