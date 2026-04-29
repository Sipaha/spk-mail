import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
import { relative } from '../lib/time'
import { client } from '../api/client'

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
  // The currently-open thread is rendered as read (no blue dot, no bold)
  // even if the pinned snapshot still has unread_count > 0. Pinning keeps the
  // row in the list while the user is reading it; muting the unread cue is
  // the standard visual feedback that "yes, you've opened this".
  const isUnread = !isOpen && t.unread_count > 0
  const sender = parseSender(t.last_from) || '(unknown)'

  return (
    <button
      onClick={() => onOpen(t.id)}
      className={`group w-full text-left px-3 py-2.5 border-b border-zinc-800 hover:bg-zinc-900/60 transition-colors flex gap-3 ${isOpen ? 'bg-zinc-900' : ''}`}>
      {/* Unread indicator: blue dot, fills the left margin if unread.
          When read, an invisible same-size span keeps row heights aligned. */}
      <span className="pt-1.5 shrink-0">
        {isUnread
          ? <span className="block size-2 rounded-full bg-blue-500" />
          : <span className="block size-2" />}
      </span>

      <div className="min-w-0 flex-1">
        {/* Top row: sender + (msg count badge) + date */}
        <div className="flex items-baseline gap-2">
          <span className={`truncate ${isUnread ? 'font-semibold text-zinc-100' : 'text-zinc-300'}`}>
            {sender}
          </span>
          {t.msg_count > 1 && (
            <span className="text-xs text-zinc-500 shrink-0">{t.msg_count}</span>
          )}
          <span className="ml-auto text-xs text-zinc-500 shrink-0">{relative(t.last_date)}</span>
        </div>

        {/* Subject row — bold for unread */}
        <div className={`truncate text-sm mt-0.5 ${isUnread ? 'text-zinc-200 font-medium' : 'text-zinc-400'}`}>
          {t.subject || '(no subject)'}
        </div>

        {/* Snippet row + flagged/attachment icons. nbsp keeps height stable
            when snippet is empty so all rows are the same size. */}
        <div className="flex items-center gap-1.5 mt-0.5">
          <span className="truncate text-xs text-zinc-500 flex-1">
            {t.snippet || ' '}
          </span>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              client.toggleThreadFlagged(t.id).catch(err => console.warn('toggleThreadFlagged failed', err))
            }}
            title={t.has_flagged ? 'Unflag thread' : 'Flag thread'}
            aria-label={t.has_flagged ? `Unflag thread "${t.subject || '(no subject)'}"` : `Flag thread "${t.subject || '(no subject)'}"`}
            className={`text-xs shrink-0 transition-colors ${
              t.has_flagged
                ? 'text-amber-400 hover:text-amber-300'
                : 'text-zinc-500 opacity-0 group-hover:opacity-100 hover:text-amber-400'
            }`}
          >
            {t.has_flagged ? '★' : '☆'}
          </button>
          {t.has_attach  && <span className="text-zinc-500 text-xs shrink-0" title="Attachment">📎</span>}
        </div>
      </div>
    </button>
  )
}
