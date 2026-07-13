import { useCallback, useEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
import { filterSig } from '../lib/filterSig'
import { PAGE_SIZE } from '../lib/paging'
import ThreadRow from './ThreadRow'

// Length of the leaving-row collapse animation. Matches the CSS transition on
// ThreadRow's wrapper (see leaveClass below). Keep these two in sync.
const LEAVE_MS = 250

type AnimRow = { thread: ThreadDTO; leaving: boolean }

export default function ThreadList() {
  const threads = useStore(s => s.threads)
  const filter = useStore(s => s.filter)
  const setThreads = useStore(s => s.setThreads)
  const setOpenThread = useStore(s => s.setOpenThread)
  const openThreadId = useStore(s => s.openThreadId)
  const activeProfileId = useStore(s => s.activeProfileId)

  const [listError, setListError] = useState<string | null>(null)
  const [hasMore, setHasMore] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  // loading: true while the initial fetch for the current filter scope is in
  // flight. Gates the empty states below — without it, "You're all caught up"
  // flashes for the fetch round-trip on every filter switch.
  const [loading, setLoading] = useState(true)
  const [retryKey, setRetryKey] = useState(0)

  // pinned: snapshot of the currently-open thread. Kept in render even if it
  // falls out of the server-filtered list (e.g. user opens an unread thread,
  // mark-read fires, server's Unread filter then excludes it). Without this
  // the row would vanish under the user's cursor while they're still reading.
  // Cleared whenever the filter context changes or the thread is closed.
  const [pinned, setPinned] = useState<ThreadDTO | undefined>()

  // rows: animated mirror of `visible`. Rows that fall out of `visible` are
  // marked `leaving=true` first and only physically removed after LEAVE_MS so
  // the user sees them fade-and-collapse instead of disappearing instantly.
  const [rows, setRows] = useState<AnimRow[]>([])
  const leaveTimers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map())

  const sig = filterSig(filter, activeProfileId)

  // Generation counter for the filter scope. Bumped every time the filter-
  // switch effect below runs (new sig, or a manual retry). `isStale()`
  // closures capture the generation active when a request started; if the
  // counter has moved on by the time the response lands, the request is for
  // a scope the user has already navigated away from and its result must be
  // discarded rather than applied to state.
  const genRef = useRef(0)

  const fetchThreads = useCallback((offset: number, append: boolean, isStale?: () => boolean) => {
    return client.listThreads({
      account_id: filter.accountId,
      folder_id: filter.folderId,
      unread_only: filter.unreadOnly,
      has_flagged: filter.hasFlagged,
      profile_id: activeProfileId ?? undefined,
      limit: PAGE_SIZE,
      offset,
    }).then(rs => {
      // A stale response (its scope's generation has moved on) must never
      // touch state — that's exactly how a delayed response from filter A
      // would overwrite the already-applied list for filter C.
      if (isStale?.()) return rs
      if (append) {
        const existing = useStore.getState().threads
        setThreads([...existing, ...rs])
      } else {
        setThreads(rs)
      }
      setHasMore(rs.length === PAGE_SIZE)
      setListError(null)
      return rs
    })
  }, [filter.accountId, filter.folderId, filter.unreadOnly, filter.hasFlagged, activeProfileId, setThreads])

  useEffect(() => {
    // Clear the store's thread list synchronously before kicking off the new
    // fetch. Without this, threads from the previous filter scope linger in
    // the store until the new request resolves — and any unrelated re-render
    // in that window (pinned cleared, an event-driven refetch fires, etc.)
    // re-derives `visible` and the diff path repopulates rows with the OLD
    // scope's data for one frame. That's the "flash of Default profile on
    // an empty profile" glitch. Clearing first guarantees the only thing the
    // user can see between switch and load is the empty state.
    setThreads([])
    setHasMore(false)
    setListError(null)
    // Generation guard: rapid filter switches (A → B → C) used to let A's
    // delayed response settle and overwrite C's already-applied list, because
    // fetchThreads applied setThreads/setHasMore unconditionally in its
    // .then. Bump the generation for this run and hand fetchThreads an
    // isStale() closure bound to it; a response only gets applied (or its
    // error surfaced) while its generation is still the current one. Errors
    // are surfaced to the user so a network failure no longer leaves them on
    // a silently-empty list.
    const myGen = ++genRef.current
    const isStale = () => genRef.current !== myGen
    setLoading(true)
    fetchThreads(0, false, isStale)
      .then(() => { if (!isStale()) setLoading(false) })
      .catch(err => {
        if (!isStale()) {
          setLoading(false)
          console.error('listThreads failed', err)
          setListError(err instanceof Error ? err.message : String(err))
        }
      })
  }, [sig, retryKey, setThreads, fetchThreads])

  const loadMore = () => {
    if (loadingMore || !hasMore) return
    setLoadingMore(true)
    // Same generation guard as the initial fetch: if the filter scope
    // changes while this load-more is in flight, its page must not be
    // appended to the new scope's (already-reset) list.
    const myGen = genRef.current
    const isStale = () => genRef.current !== myGen
    fetchThreads(threads.length, true, isStale)
      .catch(err => {
        if (!isStale()) {
          console.error('listThreads load-more failed', err)
          setListError(err instanceof Error ? err.message : String(err))
        }
      })
      .finally(() => setLoadingMore(false))
  }

  // Clear pinned snapshot when the filter context changes (user navigated to a
  // different folder/view) or when the thread is closed.
  useEffect(() => { setPinned(undefined) }, [sig])

  // Capture / refresh the pinned snapshot. Only refresh when we actually find
  // the open thread in the current filtered list — if it's already been
  // filtered out (server stopped returning it after mark-read), keep the last
  // good snapshot rather than overwriting with undefined.
  useEffect(() => {
    if (openThreadId === undefined) { setPinned(undefined); return }
    const fresh = threads.find(t => t.id === openThreadId)
    if (fresh) setPinned(fresh)
  }, [openThreadId, threads])

  // Compose the rendered list: server-filtered threads + pinned (when not
  // already in the list). Sorted by last_date DESC to keep ordering stable.
  const visible = pinned && !threads.some(t => t.id === pinned.id)
    ? [...threads, pinned].sort((a, b) => b.last_date - a.last_date)
    : threads

  const prevSig = useRef(sig)

  // Diff visible against the currently-rendered rows. Newcomers are appended
  // immediately, rows that disappear from visible flip to `leaving` and a
  // setTimeout schedules the actual removal after the CSS transition window.
  useEffect(() => {
    const sigChanged = prevSig.current !== sig
    prevSig.current = sig

    if (sigChanged) {
      // Filter context changed (profile / folder / view toggle). The store
      // still holds the PREVIOUS scope's threads until the refetch resolves,
      // so `visible` here is stale. If we snap rows=visible we'd briefly show
      // the old list and then animate every old thread out when the new data
      // lands. Instead clear rows immediately — the next render after the
      // refetch will populate via the diff path with prev=[] and treat new
      // threads as newcomers (instant, no leave-anim).
      for (const t of leaveTimers.current.values()) clearTimeout(t)
      leaveTimers.current.clear()
      setRows([])
      return
    }

    const visibleMap = new Map(visible.map(t => [t.id, t]))
    setRows(prev => {
      const seen = new Set<number>()
      const out: AnimRow[] = []
      for (const r of prev) {
        const fresh = visibleMap.get(r.thread.id)
        if (fresh) {
          out.push({ thread: fresh, leaving: false })
          seen.add(r.thread.id)
          const t = leaveTimers.current.get(r.thread.id)
          if (t) { clearTimeout(t); leaveTimers.current.delete(r.thread.id) }
        } else if (r.leaving) {
          out.push(r)
          seen.add(r.thread.id)
        } else {
          out.push({ ...r, leaving: true })
          seen.add(r.thread.id)
          const id = r.thread.id
          const tid = setTimeout(() => {
            setRows(curr => curr.filter(x => x.thread.id !== id))
            leaveTimers.current.delete(id)
          }, LEAVE_MS)
          leaveTimers.current.set(id, tid)
        }
      }
      for (const t of visible) {
        if (!seen.has(t.id)) out.push({ thread: t, leaving: false })
      }
      return out.sort((a, b) => b.thread.last_date - a.thread.last_date)
    })
  }, [visible, sig])

  // Cleanup pending timers on unmount.
  useEffect(() => () => {
    for (const t of leaveTimers.current.values()) clearTimeout(t)
    leaveTimers.current.clear()
  }, [])

  return (
    <div>
      {listError && (
        <div className="p-6 text-sm text-danger" role="alert">
          Failed to load threads: {listError}
          <button
            type="button"
            onClick={() => setRetryKey(k => k + 1)}
            className="ml-2 rounded border border-edge-strong px-2 py-0.5 text-xs text-fg-sub hover:bg-ink-800 hover:text-fg"
          >
            Retry
          </button>
        </div>
      )}
      {!listError && rows.length === 0 && (loading ? <ListLoading /> : <ListEmpty />)}
      {rows.map(({ thread: t, leaving }) => (
        <div
          key={t.id}
          className={`overflow-hidden transition-all ease-out`}
          style={{
            transitionDuration: `${LEAVE_MS}ms`,
            opacity: leaving ? 0 : 1,
            maxHeight: leaving ? 0 : 200,
            transform: leaving ? 'translateX(-12px)' : 'translateX(0)',
          }}>
          <ThreadRow t={t} onOpen={(id) => {
            // Take the pin snapshot BEFORE kicking off mark-read, so the row
            // stays under the cursor even if the server response races back
            // before our useEffect captures it.
            const fresh = threads.find(x => x.id === id)
            if (fresh) setPinned(fresh)
            client.getThread(id).then(msgs => {
              setOpenThread(id, msgs)
              const unread = msgs.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
              if (unread.length) {
                client.markRead(unread)
                  .then(() => useStore.getState().markThreadRead(id))
                  .catch(err => console.warn('markRead failed', err))
              }
            }).catch(err => console.warn('getThread failed', err))
          }} />
        </div>
      ))}
      {hasMore && !listError && (
        <div className="border-t border-edge p-4 text-center">
          <button
            type="button"
            onClick={loadMore}
            disabled={loadingMore}
            className="text-sm text-accent hover:text-accent/80 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loadingMore ? 'Loading…' : 'Load more'}
          </button>
        </div>
      )}
    </div>
  )
}

function ListLoading() {
  return <div className="p-6 text-center text-[13px] text-fg-faint">Loading…</div>
}

// ListEmpty picks copy for WHY the list is empty: no accounts configured at
// all (invite the first setup), an exhausted Unread/Flagged filter (good
// news), or simply an empty folder scope. An empty screen is an invitation
// to act, not a dead end.
function ListEmpty() {
  const accounts = useStore(s => s.accounts)
  const activeProfileId = useStore(s => s.activeProfileId)
  const filter = useStore(s => s.filter)
  const anyVisible = accounts.some(a =>
    activeProfileId === null || a.profile_id == null || a.profile_id === activeProfileId)

  if (!anyVisible) {
    return (
      <div className="flex flex-col items-center gap-3 px-6 py-16 text-center">
        <div className="text-sm font-medium text-fg-sub">No accounts in this profile</div>
        <p className="max-w-[26ch] text-xs leading-relaxed text-fg-faint">
          Connect an IMAP account to start reading mail here.
        </p>
        <a
          href="#/add-account"
          className="rounded-md bg-accent-deep px-3 py-1.5 text-[13px] font-medium text-fg hover:bg-accent-deep/80"
        >
          Add account
        </a>
      </div>
    )
  }
  const [title, hint] = filter.unreadOnly
    ? ['All caught up', 'No unread mail in this view.']
    : filter.hasFlagged
      ? ['No flagged mail', 'Flag a thread with the star to pin it here.']
      : ['Nothing here yet', 'Mail that arrives in this view will show up here.']
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-16 text-center">
      <div className="text-sm font-medium text-fg-sub">{title}</div>
      <p className="max-w-[28ch] text-xs leading-relaxed text-fg-faint">{hint}</p>
    </div>
  )
}