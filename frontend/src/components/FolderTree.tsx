import { useEffect } from 'react'
import { useShallow } from 'zustand/shallow'
import { client } from '../api/client'
import { useStore } from '../store'
import { EyeIcon, FolderIcon, ROLE_ICONS, StarIcon } from './icons'

const EMPTY_FOLDERS: never[] = []

export default function FolderTree({ accountId: ownerId }: { accountId: number }) {
  const folders = useStore(s => s.folders[ownerId] ?? EMPTY_FOLDERS)
  const setFolders = useStore(s => s.setFolders)
  // Subscribe only to the four primitives this component actually reads —
  // see AccountSidebar.tsx for the same useShallow rationale.
  const { fAccountId, fFolderId, fUnreadOnly, fHasFlagged } = useStore(
    useShallow(s => ({
      fAccountId: s.filter.accountId,
      fFolderId: s.filter.folderId,
      fUnreadOnly: s.filter.unreadOnly,
      fHasFlagged: s.filter.hasFlagged,
    })),
  )
  const setFilter = useStore(s => s.setFilter)

  useEffect(() => {
    client.listFolders(ownerId)
      .then(fs => setFolders(ownerId, fs))
      .catch(err => console.warn('listFolders failed', err))
  }, [ownerId, setFolders])

  // Aggregate counts across this account for the virtual rows.
  const totalUnread = folders.reduce((a, f) => a + (f.unread_count ?? 0), 0)
  const totalFlagged = folders.reduce((a, f) => a + (f.flagged_count ?? 0), 0)

  const isUnreadActive =
    fAccountId === ownerId && fFolderId === undefined && fUnreadOnly && !fHasFlagged
  const isFlaggedActive =
    fAccountId === ownerId && fFolderId === undefined && fHasFlagged && !fUnreadOnly

  const rowClass = (active: boolean) =>
    `w-full flex items-center gap-2 px-2 py-1 rounded hover:bg-ink-800 ${active ? 'bg-ink-800 text-fg' : 'text-fg-sub'}`

  return (
    // <nav aria-label="Folders"> turns the FolderTree into a discoverable
    // landmark — Playwright/screen-readers can address it by role+name
    // instead of brittle text/.first() selectors. Multiple <nav> elements
    // per page is allowed by ARIA so long as each carries a distinguishing
    // aria-label, which they do (one per account, all sharing the
    // "Folders" label is fine since they live in different account
    // sub-trees of the sidebar).
    <nav aria-label="Folders"><ul className="ml-2 mt-0.5 space-y-px text-xs">
      <li>
        <button
          onClick={() => setFilter({ accountId: ownerId, folderId: undefined, unreadOnly: true, hasFlagged: false })}
          className={rowClass(isUnreadActive)}>
          <EyeIcon className="size-3.5 shrink-0" />
          <span className="truncate">Unread</span>
          {totalUnread > 0 && (
            <span className="ml-auto rounded-full bg-accent-deep px-1.5 font-mono text-[10px] leading-tight text-fg">{totalUnread}</span>
          )}
        </button>
      </li>
      <li>
        <button
          onClick={() => setFilter({ accountId: ownerId, folderId: undefined, unreadOnly: false, hasFlagged: true })}
          className={rowClass(isFlaggedActive)}>
          <StarIcon className="size-3.5 shrink-0" />
          <span className="truncate">Flagged</span>
          {totalFlagged > 0 && (
            <span className="ml-auto shrink-0 font-mono text-[10px] text-brass">{totalFlagged}</span>
          )}
        </button>
      </li>
      {folders.map(f => {
        const active = fAccountId === ownerId && fFolderId === f.id
        const unread = f.unread_count ?? 0
        const total = f.total_count ?? 0
        const Icon = ROLE_ICONS[f.role] ?? FolderIcon
        return (
          <li key={f.id}>
            <button
              onClick={() => setFilter({ accountId: ownerId, folderId: f.id, unreadOnly: false, hasFlagged: false })}
              className={rowClass(active)}>
              <Icon className="size-3.5 shrink-0" />
              <span className={`truncate ${unread > 0 ? 'font-medium text-fg' : ''}`}>{f.name}</span>
              {(unread > 0 || total > 0) && (
                <span className="ml-auto flex shrink-0 items-center gap-1.5 font-mono text-[10px]">
                  {unread > 0 && (
                    // Note: <button> nested inside the row's <button> is
                    // technically invalid HTML per the W3C content model
                    // (interactive content cannot contain interactive
                    // descendants). React builds the DOM via createElement
                    // not the HTML parser, so the nested button is rendered
                    // as authored and Chromium (the Wails runtime) handles
                    // the click + stopPropagation correctly. Refactor to a
                    // sibling element if we ever need stricter HTML
                    // validity or to support non-Chromium screen readers.
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        client.markFolderRead(f.id).catch(err => console.warn('markFolderRead failed', err))
                      }}
                      title="Mark all as read"
                      aria-label={`Mark all messages in ${f.name} as read`}
                      className="size-1.5 rounded-full bg-fg-faint transition-[width,height,background-color] hover:size-2 hover:bg-accent"
                    />
                  )}
                  {unread > 0 && <span className="text-accent">{unread}</span>}
                  {total > 0 && <span className="text-fg-faint">/ {total}</span>}
                </span>
              )}
            </button>
          </li>
        )
      })}
    </ul></nav>
  )
}
