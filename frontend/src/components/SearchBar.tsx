import { useEffect, useRef, useState } from 'react'
import { useStore } from '../store'

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
    <div className="px-3 py-2 border-b border-zinc-800 sticky top-0 bg-zinc-950 z-10">
      <div className="relative">
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
          placeholder="Search… (try: from:bob has:attachment unread)"
          className="w-full rounded bg-zinc-900 border border-zinc-700 focus:border-zinc-500 px-2 py-1.5 pr-7 text-sm" />
        {value.length > 0 && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => { setValue(''); flush('') }}
            className="absolute right-1.5 top-1/2 -translate-y-1/2 size-5 leading-none text-zinc-500 hover:text-zinc-200">
            ✕
          </button>
        )}
      </div>
    </div>
  )
}
