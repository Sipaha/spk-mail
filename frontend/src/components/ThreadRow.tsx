import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
import { relative } from '../lib/time'
import { client } from '../api/client'
import { PaperclipIcon, StarIcon } from './icons'

// parseSender extracts a display name from a raw "Name <addr@example.com>"
// header value. Falls back to the addr-part, then the whole string.
function parseSender(raw: string): string {
  if (!raw) return ''
  const m = raw.match(/^\s*"?([^"<]+?)"?\s*<[^>]+>\s*$/)
  if (m && m[1].trim()) return m[1].trim()
  const m2 = raw.match(/<([^>]+)>/)
  if (m2) return m2[1]
  return raw
}

export default function ThreadRow({ t, onOpen }: { t: ThreadDTO; onOpen: (id: number) => void }) {
  const isOpen = useStore(s => s.openThreadId === t.id)
  // Account spine: only meaningful when the list actually mixes accounts (the
  // unified "All mail" view). Once the user has narrowed to one account, every
  // row would carry the same stripe — noise, not information. account_id is the
  // NEWEST message's account; a thread can span accounts.
  const unified = useStore(s => s.filter.accountId === undefined)
  const accountColor = useStore(s => s.accounts.find(a => a.id === t.account_id)?.color)
  // The currently-open thread is rendered as read (no accent dot, no bold)
  // even if the pinned snapshot still has unread_count > 0. Pinning keeps the
  // row in the list while the user is reading it; muting the unread cue is
  // the standard visual feedback that "yes, you've opened this".
  const isUnread = !isOpen && t.unread_count > 0
  const sender = parseSender(t.last_from) || '(unknown)'

  const open = () => onOpen(t.id)

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={open}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault()
          open()
        }
      }}
      className={`group relative flex w-full cursor-pointer gap-2.5 border-b border-edge py-2.5 pl-3 pr-3 text-left transition-colors hover:bg-ink-850 ${isOpen ? 'bg-ink-800' : ''}`}>
      {/* Left rail: the reading thread wins (accent); otherwise, in the unified
          view, the account's own colour — the same spine the sidebar uses, so a
          row's origin is readable without opening it. */}
      {isOpen
        ? <span aria-hidden="true" className="absolute bottom-0 left-0 top-0 w-0.5 bg-accent" />
        : unified && accountColor && (
          <span
            aria-hidden="true"
            className="absolute bottom-0 left-0 top-0 w-0.5 opacity-70"
            style={{ background: accountColor }}
          />
        )}

      {/* Unread indicator: accent dot in the left margin. When read, an
          invisible same-size span keeps row heights aligned. */}
      <span className="shrink-0 pt-1.5">
        {isUnread
          ? <span className="block size-2 rounded-full bg-accent" />
          : <span className="block size-2" />}
      </span>

      <div className="min-w-0 flex-1">
        {/* Top row: sender + (msg count) + date */}
        <div className="flex items-baseline gap-2">
          <span className={`truncate text-[13px] ${isUnread ? 'font-semibold text-fg' : 'text-fg-sub'}`}>
            {sender}
          </span>
          {t.msg_count > 1 && (
            <span className="shrink-0 font-mono text-[10px] text-fg-faint">{t.msg_count}</span>
          )}
          <span className="ml-auto shrink-0 font-mono text-[11px] text-fg-faint">{relative(t.last_date)}</span>
        </div>

        {/* Subject row — carries the unread weight */}
        <div className={`mt-0.5 truncate text-[13px] ${isUnread ? 'font-medium text-fg' : 'text-fg-sub'}`}>
          {t.subject || '(no subject)'}
        </div>

        {/* Snippet row + flagged/attachment icons. nbsp keeps height stable
            when snippet is empty so all rows are the same size. */}
        <div className="mt-0.5 flex items-center gap-1.5">
          <span className="flex-1 truncate text-xs text-fg-faint">
            {t.snippet || ' '}
          </span>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              client.toggleThreadFlagged(t.id).catch(err => console.warn('toggleThreadFlagged failed', err))
            }}
            title={t.has_flagged ? 'Unflag thread' : 'Flag thread'}
            aria-label={t.has_flagged ? `Unflag thread "${t.subject || '(no subject)'}"` : `Flag thread "${t.subject || '(no subject)'}"`}
            className={`shrink-0 transition-colors ${
              t.has_flagged
                ? 'text-brass hover:text-brass/80'
                : 'text-fg-faint opacity-0 hover:text-brass group-hover:opacity-100'
            }`}
          >
            <StarIcon className="size-3.5" filled={t.has_flagged} />
          </button>
          {t.has_attach && <PaperclipIcon className="size-3 shrink-0 text-fg-faint" />}
        </div>
      </div>
    </div>
  )
}
