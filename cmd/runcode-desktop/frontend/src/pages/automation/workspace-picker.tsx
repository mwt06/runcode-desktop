import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { errText, pickWorkspaceFolder } from '@/core/bridge'
import { basename } from '@/core/paths'
import { Banner } from '@/ui/feedback'
import { CheckMark } from '@/ui/glyphs'
import { Icon } from '@/ui/icons'

// 工作区只保存路径；当前目录、历史目录和本次浏览结果都在同一折叠菜单中。
export function WorkspacePicker({ value, current = '', recent, onChange, disabled = false }: {
  value: string
  current?: string
  recent: string[]
  onChange: (path: string) => void
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [picked, setPicked] = useState<string[]>([])
  const [error, setError] = useState('')
  const [position, setPosition] = useState({ left: 0, top: 0, width: 320, maxHeight: 320 })
  const triggerRef = useRef<HTMLButtonElement>(null)

  const options = useMemo(() => {
    const seen = new Set<string>()
    const result: string[] = []
    for (const raw of [current, value, ...recent, ...picked]) {
      const path = raw.trim()
      const key = normalizeWorkspace(path)
      if (!path || seen.has(key)) continue
      seen.add(key)
      result.push(path)
    }
    return result
  }, [current, value, recent, picked])

  function reposition() {
    const trigger = triggerRef.current
    if (!trigger) return
    const rect = trigger.getBoundingClientRect()
    const below = window.innerHeight - rect.bottom - 12
    const above = rect.top - 12
    const openUp = below < 220 && above > below
    const maxHeight = Math.max(160, Math.min(320, openUp ? above : below))
    setPosition({
      left: Math.max(8, Math.min(rect.left, window.innerWidth - rect.width - 8)),
      top: openUp ? Math.max(8, rect.top - maxHeight - 6) : rect.bottom + 6,
      width: rect.width,
      maxHeight,
    })
  }

  useEffect(() => {
    if (!open) return
    reposition()
    const update = () => reposition()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
    }
  }, [open])

  async function browse() {
    if (disabled) return
    setError('')
    try {
      const path = (await pickWorkspaceFolder()).trim()
      if (!path) return
      setPicked((prev) => [...prev, path])
      onChange(path)
      setOpen(false)
    } catch (e) {
      if (isPickerCancellation(e)) return
      setError(errText(e))
    }
  }

  function choose(path: string) {
    onChange(path)
    setOpen(false)
  }

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`关联工作区：${value || '未选择'}`}
        title={value || undefined}
        className="w-full min-w-0 flex items-center gap-2 rounded-field border border-line2 bg-surface2 px-3 py-2.5 text-left disabled:opacity-60"
        onClick={() => setOpen((next) => !next)}
      >
        <Icon name="folder" size={15} className="text-muted flex-none" />
        <span className="min-w-0 flex-1 truncate text-[13px] text-ink">
          {basename(value) || '选择工作区'}
        </span>
        <Icon name="chevron-down" size={14} className={`text-faint flex-none transition ${open ? 'rotate-180' : ''}`} />
      </button>
      {open && createPortal(
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div
            role="listbox"
            aria-label="选择工作区"
            className="fixed z-50 flex flex-col overflow-hidden rounded-[12px] border border-line2 bg-surface shadow-modal"
            style={{ left: position.left, top: position.top, width: position.width, maxHeight: position.maxHeight }}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.preventDefault()
                setOpen(false)
                triggerRef.current?.focus()
              }
            }}
          >
            <div className="min-h-0 flex-1 overflow-y-auto py-1">
              {options.map((path) => {
                const selected = normalizeWorkspace(path) === normalizeWorkspace(value)
                return (
                  <button
                    key={normalizeWorkspace(path)}
                    type="button"
                    role="option"
                    aria-selected={selected}
                    title={path}
                    className={`w-full min-w-0 px-3 py-2 text-left hover:bg-surface2 ${selected ? 'bg-primarysoft text-primaryink' : 'text-ink'}`}
                    onClick={() => choose(path)}
                  >
                    <span className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{basename(path)}</span>
                      {selected && <CheckMark size={13} className="text-primaryink flex-none" />}
                    </span>
                    <span className="mt-0.5 block truncate font-mono text-[11px] text-muted">{path}</span>
                  </button>
                )
              })}
            </div>
            <button
              type="button"
              className="flex-none border-t border-line px-3 py-2.5 text-left text-[13px] text-primaryink hover:bg-surface2"
              onClick={() => void browse()}
            >
              浏览其他目录...
            </button>
            {error && <Banner tone="danger" className="m-2">{error}</Banner>}
          </div>
        </>,
        document.body,
      )}
    </div>
  )
}

function normalizeWorkspace(path: string): string {
  return path.trim().replace(/\\/g, '/').replace(/\/+$/, '').toLowerCase()
}

function isPickerCancellation(error: unknown): boolean {
  const message = errText(error).trim().toLowerCase()
  return message === 'cancelled by user' || message === 'canceled by user'
}
