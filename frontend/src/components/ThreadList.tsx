import { useEffect, useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
import ThreadRow from './ThreadRow'

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

  return (
    <div>
      {visible.length === 0 && <div className="p-6 text-sm text-zinc-500">No threads.</div>}
      {visible.map(t => (
        <ThreadRow key={t.id} t={t} onOpen={(id) => {
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
      ))}
    </div>
  )
}
