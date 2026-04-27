import { useStore } from '../store'
import MessageBubble from './MessageBubble'

export default function ThreadView() {
  const msgs = useStore(s => s.openThread)
  const id = useStore(s => s.openThreadId)
  if (!id) return <div className="p-8 text-sm text-zinc-500">Select a thread.</div>
  if (!msgs) return <div className="p-8 text-sm text-zinc-500">Loading…</div>
  if (msgs.length === 0) return <div className="p-8 text-sm text-zinc-500">Empty thread.</div>
  return (
    <div>
      <div className="px-4 py-3 border-b border-zinc-800">
        <h2 className="text-lg font-semibold">{msgs[0].subject || '(no subject)'}</h2>
      </div>
      {msgs.map(m => <MessageBubble key={m.id} msg={m} />)}
    </div>
  )
}
