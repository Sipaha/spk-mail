import { useEffect } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'

const ROLE_ICON: Record<string, string> = {
  inbox: '📥', sent: '📤', drafts: '📝', archive: '🗃', spam: '⚠️', trash: '🗑',
}

const EMPTY_FOLDERS: never[] = []

export default function FolderTree({ accountId }: { accountId: number }) {
  const folders = useStore(s => s.folders[accountId] ?? EMPTY_FOLDERS)
  const setFolders = useStore(s => s.setFolders)
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)

  useEffect(() => {
    client.listFolders(accountId).then(fs => setFolders(accountId, fs))
  }, [accountId, setFolders])

  // Aggregate counts across this account for the virtual rows.
  const totalUnread = folders.reduce((a, f) => a + (f.unread_count ?? 0), 0)
  const totalFlagged = folders.reduce((a, f) => a + (f.flagged_count ?? 0), 0)

  const isUnreadActive =
    filter.accountId === accountId && filter.folderId === undefined && filter.unreadOnly && !filter.hasFlagged
  const isFlaggedActive =
    filter.accountId === accountId && filter.folderId === undefined && filter.hasFlagged && !filter.unreadOnly

  const rowClass = (active: boolean) =>
    `w-full flex items-center gap-2 px-2 py-1 rounded hover:bg-zinc-800 ${active ? 'bg-zinc-800' : ''}`

  return (
    <ul className="ml-4 mt-1 space-y-0.5 text-xs">
      <li>
        <button
          onClick={() => setFilter({ accountId, folderId: undefined, unreadOnly: true, hasFlagged: false })}
          className={rowClass(isUnreadActive)}>
          <span>👁</span>
          <span className="truncate">Unread</span>
          {totalUnread > 0 && (
            <span className="ml-auto rounded-full bg-blue-600 text-white px-1.5 leading-tight">{totalUnread}</span>
          )}
        </button>
      </li>
      <li>
        <button
          onClick={() => setFilter({ accountId, folderId: undefined, unreadOnly: false, hasFlagged: true })}
          className={rowClass(isFlaggedActive)}>
          <span>⭐</span>
          <span className="truncate">Flagged</span>
          {totalFlagged > 0 && (
            <span className="ml-auto rounded-full bg-amber-600 text-white px-1.5 leading-tight">{totalFlagged}</span>
          )}
        </button>
      </li>
      {folders.map(f => {
        const active = filter.accountId === accountId && filter.folderId === f.id
        const unread = f.unread_count ?? 0
        const total = f.total_count ?? 0
        return (
          <li key={f.id}>
            <button
              onClick={() => setFilter({ accountId, folderId: f.id, unreadOnly: false, hasFlagged: false })}
              className={rowClass(active)}>
              <span>{ROLE_ICON[f.role] ?? '📁'}</span>
              <span className="truncate">{f.name}</span>
              {(unread > 0 || total > 0) && (
                <span className="ml-auto text-[10px] shrink-0">
                  {unread > 0 && <span className="text-blue-400 mr-1">{unread}</span>}
                  {total > 0 && <span className="text-zinc-500">/ {total}</span>}
                </span>
              )}
            </button>
          </li>
        )
      })}
    </ul>
  )
}
