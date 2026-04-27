import { useStore } from '../store'

export default function AccountSidebar() {
  const accounts = useStore(s => s.accounts)
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)
  const activeProfileId = useStore(s => s.activeProfileId)
  const visibleAccounts = activeProfileId === null
    ? accounts
    : accounts.filter(a => a.profile_id === activeProfileId)
  return (
    <div className="p-3 space-y-1 text-sm">
      <button
        onClick={() => setFilter({ accountId: undefined })}
        className={`w-full text-left rounded px-2 py-1.5 hover:bg-zinc-800 ${filter.accountId === undefined ? 'bg-zinc-800' : ''}`}>
        Unified Inbox
      </button>
      <div className="mt-3 mb-1 text-xs uppercase tracking-wide text-zinc-500">Accounts</div>
      {visibleAccounts.map(a => (
        <button
          key={a.id}
          onClick={() => setFilter({ accountId: a.id })}
          className={`w-full flex items-center gap-2 rounded px-2 py-1.5 hover:bg-zinc-800 ${filter.accountId === a.id ? 'bg-zinc-800' : ''}`}>
          <span className="size-2.5 rounded-full" style={{ background: a.color }} />
          <span className="truncate">{a.name}</span>
          <span className={`ml-auto text-[10px] ${a.status === 'ok' ? 'text-emerald-400' : a.status === 'connecting' ? 'text-amber-400' : 'text-red-400'}`}>
            {a.status}
          </span>
        </button>
      ))}
      <a href="#/settings/accounts" className="block w-full text-center rounded border border-dashed border-zinc-700 px-2 py-1.5 text-xs text-zinc-400 hover:bg-zinc-800 mt-2">+ Add account</a>
    </div>
  )
}
