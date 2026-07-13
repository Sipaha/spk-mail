import { useEffect, useState } from 'react'
import { client } from '../api/client'
import type { SearchHitDTO } from '../api/types'
import { useStore } from '../store'
import { relative } from '../lib/time'
import Snippet from '../components/Snippet'

export default function SearchResults({ query }: { query: string }) {
  const [hits, setHits] = useState<SearchHitDTO[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [openError, setOpenError] = useState<string | null>(null)
  const setOpenThread = useStore(s => s.setOpenThread)

  useEffect(() => {
    setError(null)
    setOpenError(null)
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

  if (error) return <div className="p-6 text-sm text-danger">Search failed: {error}</div>
  if (hits === null) return <div className="p-6 text-center text-[13px] text-fg-faint">Searching…</div>
  if (hits.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 px-6 py-16 text-center">
        <div className="text-sm font-medium text-fg-sub">No results for "{query}"</div>
        <p className="max-w-[30ch] text-xs leading-relaxed text-fg-faint">
          Try fewer words, or narrow with from:, has:attachment, unread.
        </p>
      </div>
    )
  }

  return (
    <div>
      <div className="border-b border-edge px-4 py-2.5 text-xs text-fg-sub">
        <span className="font-mono text-fg">{hits.length}</span> {hits.length === 1 ? 'result' : 'results'} for <span className="text-fg">"{query}"</span>
      </div>
      {openError && (
        <div className="border-b border-edge px-4 py-2 text-sm text-danger" role="alert">
          Failed to open thread: {openError}
        </div>
      )}
      {hits.map(h => (
        <button
          key={h.message_id}
          onClick={async () => {
            if (!h.thread_id) return
            setOpenError(null)
            try {
              const msgs = await client.getThread(h.thread_id)
              // setOpenThread BEFORE navigating: hashchange fires after the
              // synchronous render that picks up the new thread, so the user
              // never sees a one-frame "Select a thread." placeholder.
              setOpenThread(h.thread_id, msgs)
              window.location.hash = '#/'
              // Mark-read mirrors ThreadList.onOpen / the deep-link path in
              // App.tsx — opening a thread (here, from a search result) is an
              // explicit user action, so unread messages in it clear.
              const unread = msgs.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
              if (unread.length) {
                const threadId = h.thread_id
                client.markRead(unread)
                  .then(() => useStore.getState().markThreadRead(threadId))
                  .catch(err => console.warn('markRead failed', err))
              }
            } catch (err) {
              setOpenError(err instanceof Error ? err.message : String(err))
            }
          }}
          className="block w-full border-b border-edge px-4 py-2.5 text-left hover:bg-ink-850">
          <div className="flex items-baseline gap-2">
            <span className="truncate text-[13px] font-medium">{h.subject || '(no subject)'}</span>
            <span className="ml-auto shrink-0 font-mono text-[11px] text-fg-faint">{relative(h.date)}</span>
          </div>
          <div className="truncate text-xs text-fg-faint">{h.from_addr}</div>
          <div className="mt-1 text-xs text-fg-sub"><Snippet text={h.snippet} /></div>
        </button>
      ))}
    </div>
  )
}
