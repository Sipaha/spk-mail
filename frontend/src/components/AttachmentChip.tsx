import { useState } from 'react'
import { client } from '../api/client'
import type { AttachmentDTO } from '../api/types'

// formatSize renders a byte count in B / KB / MB. Sub-1KB attachments
// (favicons, signature images) used to show "0 KB" via Math.round; report
// the actual byte value instead.
function formatSize(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export default function AttachmentChip({ a }: { a: AttachmentDTO }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()

  const open = async () => {
    setBusy(true); setErr(undefined)
    try { await client.openAttachment(a.id) }
    catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }

  return (
    <button
      type="button" onClick={open} disabled={!a.downloaded || busy}
      title={a.downloaded ? 'Open with system handler' : 'Downloading…'}
      className={`text-xs rounded border px-2 py-1 inline-flex items-center gap-1 ${
        a.downloaded ? 'border-zinc-700 hover:bg-zinc-800' : 'border-zinc-800 text-zinc-500 cursor-progress'
      }`}>
      <span>📎</span>
      <span className="truncate max-w-[12rem]">{a.filename}</span>
      <span className="text-zinc-500">({formatSize(a.size_bytes)})</span>
      {!a.downloaded && <span className="ml-1 size-1.5 rounded-full bg-amber-400 animate-pulse" />}
      {err && <span className="text-red-400 ml-1">!</span>}
    </button>
  )
}
