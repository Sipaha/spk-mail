import { useEffect, useRef, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
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

  useEffect(() => {
    client.listThreads({
      account_id: filter.accountId,
      folder_id: filter.folderId,
      unread_only: filter.unreadOnly,
      has_flagged: filter.hasFlagged,
      profile_id: activeProfileId ?? undefined,
      limit: 200,
    }).then(setThreads)
  }, [filter.accountId, filter.folderId, filter.unreadOnly, filter.hasFlagged, activeProfileId, setThreads])

  // Clear pinned snapshot when the filter context changes (user navigated to a
  // different folder/view) or when the thread is closed.
  useEffect(() => { setPinned(undefined) },
    [filter.accountId, filter.folderId, filter.unreadOnly, filter.hasFlagged, activeProfileId])

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

  // Diff visible against the currently-rendered rows. Newcomers are appended
  // immediately, rows that disappear from visible flip to `leaving` and a
  // setTimeout schedules the actual removal after the CSS transition window.
  useEffect(() => {
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
  }, [visible])

  // Cleanup pending timers on unmount.
  useEffect(() => () => {
    for (const t of leaveTimers.current.values()) clearTimeout(t)
    leaveTimers.current.clear()
  }, [])

  return (
    <div>
      {rows.length === 0 && <div className="p-6 text-sm text-zinc-500">No threads.</div>}
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
              const unread = msgs.filter(m => !m.flags.includes('\\Seen')).map(m => m.id)
              if (unread.length) client.markRead(unread).then(() => useStore.getState().markThreadRead(id))
            })
          }} />
        </div>
      ))}
    </div>
  )
}
