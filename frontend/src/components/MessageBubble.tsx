import { useState } from 'react'
import type { MessageDTO, RawMessageDTO } from '../api/types'
import MessageBody from './MessageBody'
import AttachmentChip from './AttachmentChip'
import MessageMenu from './MessageMenu'
import RawMessageDialog from './RawMessageDialog'
import { client } from '../api/client'
import { triggerDownload } from '../lib/download'
import { relative } from '../lib/time'

// Deterministic hue per sender so repeat correspondents keep a stable avatar
// color across threads. Saturation/lightness are pinned to the ink palette's
// register so no hash value can produce a jarring swatch. Defensive against
// a missing from_addr (malformed message rows omit the header).
function senderHue(addr: string | undefined): number {
  if (!addr) return 220
  let h = 0
  for (let i = 0; i < addr.length; i++) h = (h * 31 + addr.charCodeAt(i)) >>> 0
  return h % 360
}

function initialOf(addr: string | undefined): string {
  const m = (addr ?? '').match(/[\p{L}\p{N}]/u)
  return (m?.[0] ?? '?').toUpperCase()
}

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

  const hue = senderHue(msg.from_addr)

  return (
    <div className="border-b border-edge">
      <div className="flex items-start gap-3 px-5 pt-4">
        <span
          aria-hidden="true"
          className="mt-0.5 flex size-7 shrink-0 select-none items-center justify-center rounded-full text-xs font-semibold"
          style={{
            background: `oklch(0.32 0.06 ${hue})`,
            color: `oklch(0.85 0.05 ${hue})`,
          }}
        >
          {initialOf(msg.from_addr)}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className="truncate text-[13px] font-medium text-fg">{msg.from_addr}</span>
            <span className="ml-auto shrink-0 font-mono text-[11px] text-fg-faint">{relative(msg.date)}</span>
            <MessageMenu onViewRaw={onViewRaw} onDownloadEml={onDownload} />
          </div>
          {(msg.to_addrs ?? []).length > 0 && (
            <div className="truncate text-xs text-fg-faint">to {(msg.to_addrs ?? []).join(', ')}</div>
          )}
        </div>
      </div>
      {busy && <div className="px-5 pt-1 text-xs italic text-fg-faint">Loading raw…</div>}
      {err && <div className="px-5 pt-1 text-xs text-danger">Raw fetch failed: {err}</div>}
      <MessageBody msg={msg} />
      {msg.attachments.length > 0 && (
        <div className="flex flex-wrap gap-2 px-5 pb-4">
          {msg.attachments.map(a => (
            <AttachmentChip key={a.id} a={a} />
          ))}
        </div>
      )}
      {raw && <RawMessageDialog dto={raw} onClose={() => setRaw(null)} />}
    </div>
  )
}
