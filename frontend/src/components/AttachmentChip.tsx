import { useState } from 'react'
import { client } from '../api/client'
import type { AttachmentDTO } from '../api/types'
import { PaperclipIcon } from './icons'

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
        a.downloaded ? 'border-edge-strong text-fg-sub hover:bg-ink-800 hover:text-fg' : 'border-edge text-fg-faint cursor-progress'
      }`}>
      <PaperclipIcon className="size-3 shrink-0" />
      <span className="truncate max-w-[12rem]">{a.filename}</span>
      <span className="font-mono text-[11px] text-fg-faint">({formatSize(a.size_bytes)})</span>
      {!a.downloaded && <span className="ml-1 size-1.5 rounded-full bg-warn animate-pulse" />}
      {err && <span className="text-danger ml-1">!</span>}
    </button>
  )
}
