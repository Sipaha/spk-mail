import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'
import { relative } from '../lib/time'

export default function ThreadRow({ t, onOpen }: { t: ThreadDTO; onOpen: (id: number) => void }) {
  const isUnread = t.unread_count > 0
  const isOpen = useStore(s => s.openThreadId === t.id)
  return (
    <button
      onClick={() => onOpen(t.id)}
      className={`w-full text-left px-3 py-2 border-b border-zinc-800 hover:bg-zinc-900/60 ${isOpen ? 'bg-zinc-900' : ''}`}>
      <div className="flex items-center gap-2">
        <span className={`truncate ${isUnread ? 'font-semibold text-zinc-100' : 'text-zinc-300'}`}>
          {t.subject || '(no subject)'}
        </span>
        <span className="ml-auto text-xs text-zinc-500">{relative(t.last_date)}</span>
      </div>
      <div className="flex items-center gap-2 text-xs text-zinc-500 mt-0.5">
        {t.msg_count > 1 && <span>{t.msg_count}</span>}
        {t.has_flagged && <span>★</span>}
        {t.has_attach && <span>📎</span>}
      </div>
    </button>
  )
}
