import { useId, useMemo, useRef, useState, type FocusEvent, type KeyboardEvent } from 'react'
import { Icon } from '@/ui/icons'
import type { SettingsCategory, SettingsSection, SettingsSectionId } from './navigation-model'

type SearchResult = {
  category: SettingsCategory
  section: SettingsSection
}

const CATEGORY_ICON: Record<SettingsCategory['id'], string> = {
  account: 'bot',
  models: 'sparkles',
  media: 'file-image',
  environment: 'terminal',
  about: 'shield',
}

export function SettingsSearch({
  categories,
  onSelect,
}: {
  categories: readonly SettingsCategory[]
  onSelect: (id: SettingsSectionId) => void
}) {
  const listId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)

  const results = useMemo<SearchResult[]>(() => {
    const needle = query.trim().toLocaleLowerCase('zh-CN')
    if (!needle) return []
    return categories.flatMap((category) => category.sections
      .filter((section) => (
        section.title.toLocaleLowerCase('zh-CN').includes(needle)
        || category.title.toLocaleLowerCase('zh-CN').includes(needle)
      ))
      .map((section) => ({ category, section })))
  }, [categories, query])

  const choose = (result: SearchResult) => {
    setQuery('')
    setOpen(false)
    inputRef.current?.blur()
    onSelect(result.section.id)
  }

  const handleBlur = (event: FocusEvent<HTMLDivElement>) => {
    const next = event.relatedTarget
    if (next instanceof Node && event.currentTarget.contains(next)) return
    setOpen(false)
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      setQuery('')
      setOpen(false)
      return
    }
    if (!open || results.length === 0) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex((current) => Math.min(results.length - 1, current + 1))
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex((current) => Math.max(0, current - 1))
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      choose(results[Math.min(activeIndex, results.length - 1)])
    }
  }

  const showResults = open && query.trim().length > 0
  return (
    <div className="settings-search" onBlur={handleBlur}>
      <div className="settings-search-field">
        <Icon name="search" size={15} className="settings-search-icon" />
        <input
          ref={inputRef}
          type="search"
          role="combobox"
          aria-label="搜索设置"
          aria-autocomplete="list"
          aria-expanded={showResults}
          aria-controls={listId}
          aria-activedescendant={showResults && results[activeIndex] ? `${listId}-${activeIndex}` : undefined}
          placeholder="搜索设置"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value)
            setActiveIndex(0)
            setOpen(true)
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={handleKeyDown}
        />
        {query && (
          <button
            type="button"
            className="settings-search-clear"
            aria-label="清除搜索"
            onClick={() => {
              setQuery('')
              setOpen(false)
              inputRef.current?.focus()
            }}
          >
            ×
          </button>
        )}
      </div>
      {showResults && (
        <div id={listId} className="settings-search-results" role="listbox" aria-label="设置搜索结果">
          {results.length === 0 ? (
            <div className="settings-search-empty" role="status">没有找到匹配的设置项</div>
          ) : results.map((result, index) => (
            <button
              key={result.section.id}
              id={`${listId}-${index}`}
              type="button"
              role="option"
              aria-selected={index === activeIndex}
              className={`settings-search-result${index === activeIndex ? ' settings-search-result-active' : ''}`}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => choose(result)}
            >
              <Icon name={CATEGORY_ICON[result.category.id]} size={16} className="settings-search-result-icon" />
              <span className="settings-search-result-copy">
                <span className="settings-search-result-title">{result.section.title}</span>
                <span className="settings-search-result-category">{result.category.title}</span>
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
