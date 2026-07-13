import { useStore } from '../store'
import MessageBubble from './MessageBubble'
import { EnvelopeIcon } from './icons'

function Placeholder({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-8 text-center">
      <EnvelopeIcon className="size-8 text-fg-faint/60" />
      <div className="text-sm font-medium text-fg-sub">{title}</div>
      {hint && <p className="max-w-[32ch] text-xs leading-relaxed text-fg-faint">{hint}</p>}
    </div>
  )
}

export default function ThreadView() {
  const msgs = useStore(s => s.openThread)
  const id = useStore(s => s.openThreadId)
  const accounts = useStore(s => s.accounts)
  if (!id) return <Placeholder title="No conversation open" hint="Pick a thread from the list to read it here." />
  if (!msgs) return <Placeholder title="Loading…" />
  if (msgs.length === 0) return <Placeholder title="Empty thread" hint="This conversation has no messages." />

  // The account spine, third echo: the reading pane names which account the
  // open conversation belongs to, in that account's color.
  const account = accounts.find(a => a.id === msgs[0].account_id)

  return (
    <div>
      <div className="sticky top-0 z-10 border-b border-edge bg-ink-950/95 px-5 py-3 backdrop-blur">
        <h2 className="text-base font-semibold leading-snug">{msgs[0].subject || '(no subject)'}</h2>
        <div className="mt-1 flex items-center gap-3 font-mono text-[11px] text-fg-faint">
          {account && (
            <span className="flex items-center gap-1.5">
              <span className="size-2 rounded-full" style={{ background: account.color }} />
              {account.name}
            </span>
          )}
          <span>{msgs.length === 1 ? '1 message' : `${msgs.length} messages`}</span>
        </div>
      </div>
      {msgs.map(m => <MessageBubble key={m.id} msg={m} />)}
    </div>
  )
}
