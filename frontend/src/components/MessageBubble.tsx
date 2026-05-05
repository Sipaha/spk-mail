import { useState } from 'react'
import type { MessageDTO, RawMessageDTO } from '../api/types'
import MessageBody from './MessageBody'
import AttachmentChip from './AttachmentChip'
import MessageMenu from './MessageMenu'
import RawMessageDialog from './RawMessageDialog'
import { client } from '../api/client'
import { triggerDownload } from '../lib/download'
import { relative } from '../lib/time'

export default function MessageBubble({ msg }: { msg: MessageDTO }) {
  const [raw, setRaw] = useState<RawMessageDTO | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()

  const fetchRaw = async (): Promise<RawMessageDTO | null> => {
    setBusy(true); setErr(undefined)
    try {
      return await client.getRawMessage(msg.id)
    } catch (e) {
      setErr(String(e))
      return null
    } finally {
      setBusy(false)
    }
  }

  const onViewRaw = async () => {
    const dto = await fetchRaw()
    if (dto) setRaw(dto)
  }

  const onDownload = async () => {
    const dto = await fetchRaw()
    if (dto) triggerDownload(dto.raw_b64, dto.filename)
  }

  return (
    <div className="border-b border-zinc-800">
      <div className="flex items-baseline gap-2 px-4 pt-3">
        <span className="font-medium text-zinc-100">{msg.from_addr}</span>
        <span className="text-xs text-zinc-500 ml-auto">{relative(msg.date)}</span>
        <MessageMenu onViewRaw={onViewRaw} onDownloadEml={onDownload} />
      </div>
      <div className="px-4 text-xs text-zinc-500">to {msg.to_addrs.join(', ')}</div>
      {busy && <div className="px-4 text-xs text-zinc-500 italic">Loading raw…</div>}
      {err && <div className="px-4 text-xs text-red-400">Raw fetch failed: {err}</div>}
      <MessageBody msg={msg} />
      {msg.attachments.length > 0 && (
        <div className="px-4 pb-3 flex flex-wrap gap-2">
          {msg.attachments.map(a => (
            <AttachmentChip key={a.id} a={a} />
          ))}
        </div>
      )}
      {raw && <RawMessageDialog dto={raw} onClose={() => setRaw(null)} />}
    </div>
  )
}
