import { useShallow } from 'zustand/shallow'
import { useStore } from '../store'
import FolderTree from './FolderTree'

export default function AccountSidebar() {
  const accounts = useStore(s => s.accounts)
  // Subscribe only to the filter primitives this component actually reads
  // (the bg-zinc-800 active-state check). useShallow short-circuits on equal
  // values so a `setFilter` that doesn't change these specific fields no
  // longer re-renders the whole sidebar.
  const { accountId, folderId, unreadOnly, hasFlagged } = useStore(
    useShallow(s => ({
      accountId: s.filter.accountId,
      folderId: s.filter.folderId,
      unreadOnly: s.filter.unreadOnly,
      hasFlagged: s.filter.hasFlagged,
    })),
  )
  const setFilter = useStore(s => s.setFilter)
  const activeProfileId = useStore(s => s.activeProfileId)
  const syncProgress = useStore(s => s.syncProgress)
  const visibleAccounts = activeProfileId === null
    ? []
    : accounts.filter(a => a.profile_id === activeProfileId)
  return (
    <div className="p-3 space-y-2 text-sm">
      <div className="text-xs uppercase tracking-wide text-zinc-500">Accounts</div>
      {visibleAccounts.map(a => {
        const progress = syncProgress[a.id]
        const isSyncing = progress != null && progress.total > 0 && progress.done < progress.total
        return (
          <div key={a.id}>
            <button
              onClick={() => setFilter({ accountId: a.id, folderId: undefined, unreadOnly: false, hasFlagged: false })}
              className={`w-full flex items-center gap-2 rounded px-2 py-1.5 hover:bg-zinc-800 ${accountId === a.id && folderId === undefined && !unreadOnly && !hasFlagged ? 'bg-zinc-800' : ''}`}>
              <span className="size-2.5 rounded-full" style={{ background: a.color }} />
              <span className="truncate">{a.name}</span>
              <span className={`ml-auto text-[10px] ${a.status === 'ok' ? 'text-emerald-400' : a.status === 'connecting' ? 'text-amber-400' : 'text-red-400'}`}>
                {a.status}
              </span>
            </button>
            {/* Sync status line: always rendered (with meaningful text in both
                states) so the folder tree below never jumps when sync starts
                or finishes. Idle shows a soft "all synced" affordance instead
                of empty whitespace, so users aren't left wondering. */}
            <div
              className={`text-[10px] px-3 py-0.5 truncate pointer-events-none ${
                isSyncing ? 'text-zinc-500' : 'text-emerald-500/70'
              }`}>
              {isSyncing
                ? `Syncing ${progress.folder}: ${progress.done.toLocaleString()} / ${progress.total.toLocaleString()}`
                : '✓ All synced'}
            </div>
            <FolderTree accountId={a.id} />
          </div>
        )
      })}
      <a href="#/add-account" className="block w-full text-center rounded border border-dashed border-zinc-700 px-2 py-1.5 text-xs text-zinc-400 hover:bg-zinc-800 mt-2">+ Add account</a>
    </div>
  )
}
