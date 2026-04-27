import { useEffect } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import ThreadRow from './ThreadRow'

export default function ThreadList() {
  const threads = useStore(s => s.threads)
  const filter = useStore(s => s.filter)
  const setThreads = useStore(s => s.setThreads)
  const setOpenThread = useStore(s => s.setOpenThread)
  const activeProfileId = useStore(s => s.activeProfileId)

  useEffect(() => {
    client.listThreads({
      account_id: filter.accountId,
      unread_only: filter.unreadOnly,
      profile_id: activeProfileId ?? undefined,
      limit: 200,
    }).then(setThreads)
  }, [filter.accountId, filter.unreadOnly, activeProfileId, setThreads])

  return (
    <div>
      {threads.length === 0 && <div className="p-6 text-sm text-zinc-500">No threads.</div>}
      {threads.map(t => (
        <ThreadRow key={t.id} t={t} onOpen={(id) => {
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
