import type { MessageDTO } from '../api/types'
import MessageBody from './MessageBody'
import { relative } from '../lib/time'

export default function MessageBubble({ msg }: { msg: MessageDTO }) {
  return (
    <div className="border-b border-zinc-800">
      <div className="flex items-baseline gap-2 px-4 pt-3">
        <span className="font-medium text-zinc-100">{msg.from_addr}</span>
        <span className="text-xs text-zinc-500 ml-auto">{relative(msg.date)}</span>
      </div>
      <div className="px-4 text-xs text-zinc-500">to {msg.to_addrs.join(', ')}</div>
      <MessageBody msg={msg} />
      {msg.attachments.length > 0 && (
        <div className="px-4 pb-3 flex flex-wrap gap-2">
          {msg.attachments.map(a => (
            <span key={a.id} className="text-xs rounded border border-zinc-700 px-2 py-1">
              📎 {a.filename} <span className="text-zinc-500">({Math.round(a.size_bytes/1024)} KB)</span>
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
