import { useStore } from '../store'
import FolderTree from './FolderTree'

export default function AccountSidebar() {
  const accounts = useStore(s => s.accounts)
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)
  const activeProfileId = useStore(s => s.activeProfileId)
  const visibleAccounts = activeProfileId === null
    ? []
    : accounts.filter(a => a.profile_id === activeProfileId)
  return (
    <div className="p-3 space-y-2 text-sm">
      <div className="text-xs uppercase tracking-wide text-zinc-500">Accounts</div>
      {visibleAccounts.map(a => (
        <div key={a.id}>
          <button
            onClick={() => setFilter({ accountId: a.id, folderId: undefined, unreadOnly: false, hasFlagged: false })}
            className={`w-full flex items-center gap-2 rounded px-2 py-1.5 hover:bg-zinc-800 ${filter.accountId === a.id && filter.folderId === undefined ? 'bg-zinc-800' : ''}`}>
            <span className="size-2.5 rounded-full" style={{ background: a.color }} />
            <span className="truncate">{a.name}</span>
            <span className={`ml-auto text-[10px] ${a.status === 'ok' ? 'text-emerald-400' : a.status === 'connecting' ? 'text-amber-400' : 'text-red-400'}`}>
              {a.status}
            </span>
          </button>
          <FolderTree accountId={a.id} />
        </div>
      ))}
      <a href="#/settings/accounts" className="block w-full text-center rounded border border-dashed border-zinc-700 px-2 py-1.5 text-xs text-zinc-400 hover:bg-zinc-800 mt-2">+ Add account</a>
    </div>
  )
}
