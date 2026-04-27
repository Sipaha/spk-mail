import { useStore } from '../store'

export default function ViewSwitcher() {
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)

  const setView = (view: 'unread' | 'flagged' | null) => {
    setFilter({
      accountId: undefined, folderId: undefined,
      unreadOnly: view === 'unread',
      hasFlagged: view === 'flagged',
    })
  }
  const isUnread  = filter.unreadOnly && !filter.hasFlagged
  const isFlagged = filter.hasFlagged && !filter.unreadOnly

  return (
    <div className="flex gap-1 px-3 py-2 border-b border-zinc-800 text-xs">
      <button onClick={() => setView('unread')}
        className={`flex-1 px-2 py-1 rounded ${isUnread ? 'bg-blue-600 text-white' : 'hover:bg-zinc-800 text-zinc-300'}`}>
        Unread
      </button>
      <button onClick={() => setView('flagged')}
        className={`flex-1 px-2 py-1 rounded ${isFlagged ? 'bg-amber-600 text-white' : 'hover:bg-zinc-800 text-zinc-300'}`}>
        Flagged
      </button>
    </div>
  )
}
