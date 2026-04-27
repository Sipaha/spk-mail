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

  return (
    <ul className="ml-4 mt-1 space-y-0.5 text-xs">
      {folders.map(f => {
        const active = filter.accountId === accountId && filter.folderId === f.id
        return (
          <li key={f.id}>
            <button
              onClick={() => setFilter({ accountId, folderId: f.id, unreadOnly: false, hasFlagged: false })}
              className={`w-full flex items-center gap-2 px-2 py-1 rounded hover:bg-zinc-800 ${active ? 'bg-zinc-800' : ''}`}>
              <span>{ROLE_ICON[f.role] ?? '📁'}</span>
              <span className="truncate">{f.name}</span>
              {f.unread_count > 0 && (
                <span className="ml-auto rounded-full bg-blue-600 text-white px-1.5 leading-tight">
                  {f.unread_count}
                </span>
              )}
            </button>
          </li>
        )
      })}
    </ul>
  )
}
