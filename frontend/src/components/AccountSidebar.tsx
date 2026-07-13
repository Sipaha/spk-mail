import { useEffect, useState } from 'react'
import { useShallow } from 'zustand/shallow'
import { client } from '../api/client'
import { useStore } from '../store'
import FolderTree from './FolderTree'
import { AlertIcon, EnvelopeIcon } from './icons'

type Menu = { accountId: number; x: number; y: number }

export default function AccountSidebar() {
  const accounts = useStore(s => s.accounts)
  const setAccounts = useStore(s => s.setAccounts)
  // Subscribe only to the filter primitives this component actually reads
  // (the active-state checks). useShallow short-circuits on equal values so
  // a `setFilter` that doesn't change these specific fields no longer
  // re-renders the whole sidebar.
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
  const accountDetail = useStore(s => s.accountDetail)
  const folders = useStore(s => s.folders)
  const [menu, setMenu] = useState<Menu | null>(null)

  useEffect(() => {
    if (!menu) return
    const onDown = () => setMenu(null)
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setMenu(null) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [menu])

  // Accounts with no profile stay visible in EVERY profile: an account whose
  // profile was deleted out from under it (or one added over the raw API
  // without a profile_id) must never become unreachable from the window.
  const visibleAccounts = accounts.filter(a =>
    activeProfileId === null || a.profile_id == null || a.profile_id === activeProfileId)

  const allMailActive = accountId === undefined && folderId === undefined && !unreadOnly && !hasFlagged
  const allUnread = visibleAccounts.reduce(
    (sum, a) => sum + (folders[a.id] ?? []).reduce((x, f) => x + (f.unread_count ?? 0), 0), 0)

  const onRemove = async (id: number) => {
    setMenu(null)
    const target = accounts.find(a => a.id === id)
    if (!target) return
    if (!window.confirm(`Remove account "${target.name}" (${target.email})? This stops sync and deletes stored credentials.`)) return
    try {
      await client.removeAccount(id)
      setAccounts(await client.listAccounts())
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      window.alert(`Failed to remove account: ${msg}`)
    }
  }

  return (
    <div className="space-y-1 px-2 py-2 text-sm">
      {/* Unified view across every visible account — the app's default scope
          finally gets a row of its own, so users can come BACK to it after
          narrowing to one account or folder. */}
      <button
        onClick={() => setFilter({ accountId: undefined, folderId: undefined, unreadOnly: false, hasFlagged: false })}
        className={`flex w-full items-center gap-2 rounded px-2 py-1.5 text-[13px] hover:bg-ink-800 ${allMailActive ? 'bg-ink-800 text-fg' : 'text-fg-sub'}`}
      >
        <EnvelopeIcon className="size-4 shrink-0" />
        <span className="font-medium">All mail</span>
        {allUnread > 0 && (
          <span className="ml-auto rounded-full bg-accent-deep px-1.5 font-mono text-[10px] leading-tight text-fg">{allUnread}</span>
        )}
      </button>

      {visibleAccounts.map(a => {
        const progress = syncProgress[a.id]
        const isSyncing = progress != null && progress.total > 0 && progress.done < progress.total
        const isError = a.status === 'error'
        const isConnecting = a.status === 'connecting' || a.status === 'starting'
        const accountActive = accountId === a.id && folderId === undefined && !unreadOnly && !hasFlagged
        return (
          // The account spine: a rail in the account's color down the whole
          // block, so folders visually belong to their account and the color
          // identity matches the chips in Settings and the thread header.
          <section
            key={a.id}
            aria-label={`Account ${a.name}`}
            className="relative mt-2 pl-2.5"
          >
            <span
              aria-hidden="true"
              className="absolute bottom-1 left-0 top-1 w-0.5 rounded-full"
              style={{ background: isError ? 'var(--color-danger)' : a.color }}
            />
            <button
              onClick={() => setFilter({ accountId: a.id, folderId: undefined, unreadOnly: false, hasFlagged: false })}
              onContextMenu={(e) => {
                e.preventDefault()
                setMenu({ accountId: a.id, x: e.clientX, y: e.clientY })
              }}
              className={`flex w-full items-center gap-2 rounded px-2 py-1.5 hover:bg-ink-800 ${accountActive ? 'bg-ink-800' : ''}`}
            >
              <span className="truncate text-[13px] font-semibold">{a.name}</span>
              {isError && <AlertIcon className="size-3.5 shrink-0 text-danger" />}
            </button>
            {/* Status line: always rendered (with meaningful text in every
                state) so the folder tree below never jumps when sync starts
                or finishes. Error beats sync/idle — a broken account must
                never claim to be synced. */}
            {isError ? (
              <a
                href="#/settings"
                // Live event detail wins; a.detail is what ListAccounts knew at
                // open time — on a cold start that is all we have.
                title={accountDetail[a.id] ?? a.detail ?? 'Connection failed'}
                className="block truncate px-2 py-0.5 font-mono text-[10px] text-danger hover:underline"
              >
                Can't connect — details in Settings
              </a>
            ) : (
              <div className={`pointer-events-none truncate px-2 py-0.5 font-mono text-[10px] ${isSyncing || isConnecting ? 'text-fg-sub' : 'text-fg-faint'}`}>
                {isSyncing
                  ? `Syncing ${progress.folder} ${progress.done.toLocaleString()}/${progress.total.toLocaleString()}`
                  : isConnecting
                    ? 'Connecting…'
                    : 'Synced'}
              </div>
            )}
            <FolderTree accountId={a.id} />
          </section>
        )
      })}

      {menu && (
        <div
          role="menu"
          // Stop the document-level mousedown handler from running when
          // clicks land INSIDE the menu — otherwise the menu closes before
          // the buttons below register their own click.
          onMouseDown={(e) => e.stopPropagation()}
          className="fixed z-50 min-w-[180px] rounded-md border border-edge-strong bg-ink-850 py-1 text-xs shadow-lg"
          style={{
            left: Math.min(menu.x, window.innerWidth - 188),
            top: Math.min(menu.y, window.innerHeight - 76),
          }}
        >
          <a
            href="#/settings"
            onClick={() => setMenu(null)}
            className="block w-full px-3 py-1.5 text-left text-fg-sub hover:bg-ink-800 hover:text-fg"
          >
            Account settings…
          </a>
          <button
            type="button"
            onClick={() => onRemove(menu.accountId)}
            className="block w-full px-3 py-1.5 text-left text-danger hover:bg-ink-800"
          >
            Remove account…
          </button>
        </div>
      )}
    </div>
  )
}
