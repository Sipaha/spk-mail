import { useEffect, useState } from 'react'
import { client } from '../api/client'
import type { SearchHitDTO } from '../api/types'
import { useStore } from '../store'
import { relative } from '../lib/time'
import Snippet from '../components/Snippet'

export default function SearchResults({ query }: { query: string }) {
  const [hits, setHits] = useState<SearchHitDTO[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const setOpenThread = useStore(s => s.setOpenThread)

  useEffect(() => {
    setError(null)
    if (!query.trim()) { setHits([]); return }
    let cancelled = false
    setHits(null)
    client.search(query, 100, 0)
      .then(rs => { if (!cancelled) setHits(rs) })
      .catch(err => {
        if (cancelled) return
        setHits([])
        setError(err instanceof Error ? err.message : String(err))
      })
    return () => { cancelled = true }
  }, [query])

  if (error) return <div className="p-6 text-sm text-rose-400">Search failed: {error}</div>
  if (hits === null) return <div className="p-6 text-sm text-zinc-500">Searching…</div>
  if (hits.length === 0) return <div className="p-6 text-sm text-zinc-500">No results for "{query}".</div>

  return (
    <div>
      <div className="px-4 py-3 border-b border-zinc-800 text-sm text-zinc-400">
        {hits.length} results for <span className="text-zinc-200">"{query}"</span>
      </div>
      {hits.map(h => (
        <button
          key={h.message_id}
          onClick={async () => {
            if (h.thread_id) {
              const msgs = await client.getThread(h.thread_id)
              // setOpenThread BEFORE navigating: hashchange fires after the
              // synchronous render that picks up the new thread, so the user
              // never sees a one-frame "Select a thread." placeholder.
              setOpenThread(h.thread_id, msgs)
              window.location.hash = '#/'
            }
          }}
          className="block w-full text-left px-4 py-2 border-b border-zinc-800 hover:bg-zinc-900">
          <div className="flex items-baseline gap-2">
            <span className="font-medium truncate">{h.subject || '(no subject)'}</span>
            <span className="text-xs text-zinc-500 ml-auto">{relative(h.date)}</span>
          </div>
          <div className="text-xs text-zinc-500">{h.from_addr}</div>
          <div className="text-sm mt-1"><Snippet text={h.snippet} /></div>
        </button>
      ))}
    </div>
  )
}
