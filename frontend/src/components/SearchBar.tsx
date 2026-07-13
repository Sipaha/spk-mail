import { useEffect, useRef, useState } from 'react'
import { useStore } from '../store'
import { SearchIcon } from './icons'

const SEARCH_DEBOUNCE_MS = 250

export default function SearchBar() {
  const search = useStore(s => s.search)
  const setSearch = useStore(s => s.setSearch)
  const [value, setValue] = useState(search)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Re-sync local state when the store changes from elsewhere (e.g. routing
  // resets the query). Skip when the change originated from our own debounced
  // push to avoid stomping in-flight keystrokes.
  useEffect(() => {
    setValue(prev => (prev === search ? prev : search))
  }, [search])

  useEffect(() => () => { if (timer.current) clearTimeout(timer.current) }, [])

  const flush = (next: string) => {
    if (timer.current) { clearTimeout(timer.current); timer.current = null }
    setSearch(next)
  }

  return (
    <div className="sticky top-0 z-10 border-b border-edge bg-ink-950/95 px-3 py-2 backdrop-blur">
      <div className="relative">
        <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-fg-faint" />
        <input
          // type="text" rather than "search": the native search-input
          // decoration paints a webkit ✕ that duplicates our custom
          // clear button, and there's no portable way to suppress it
          // without per-engine CSS.
          type="text"
          value={value}
          onChange={(e) => {
            const next = e.target.value
            setValue(next)
            if (timer.current) clearTimeout(timer.current)
            timer.current = setTimeout(() => setSearch(next), SEARCH_DEBOUNCE_MS)
          }}
          onKeyDown={(e) => { if (e.key === 'Enter') flush(value) }}
          placeholder="Search… from:bob has:attachment unread"
          className="w-full rounded-md border border-edge-strong bg-ink-850 py-1.5 pl-8 pr-7 text-[13px] placeholder:text-fg-faint focus:border-accent/60" />
        {value.length > 0 && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => { setValue(''); flush('') }}
            className="absolute right-1.5 top-1/2 size-5 -translate-y-1/2 leading-none text-fg-faint hover:text-fg">
            ✕
          </button>
        )}
      </div>
    </div>
  )
}
