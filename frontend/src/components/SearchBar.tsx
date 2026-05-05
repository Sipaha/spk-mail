import { useStore } from '../store'

export default function SearchBar() {
  const search = useStore(s => s.search)
  const setSearch = useStore(s => s.setSearch)
  return (
    <div className="px-3 py-2 border-b border-zinc-800 sticky top-0 bg-zinc-950 z-10">
      <div className="relative">
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search… (try: from:bob has:attachment unread)"
          className="w-full rounded bg-zinc-900 border border-zinc-700 focus:border-zinc-500 px-2 py-1.5 pr-7 text-sm" />
        {search.length > 0 && (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => setSearch('')}
            className="absolute right-1.5 top-1/2 -translate-y-1/2 size-5 leading-none text-zinc-500 hover:text-zinc-200">
            ✕
          </button>
        )}
      </div>
    </div>
  )
}
