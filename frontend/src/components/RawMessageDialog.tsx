import { useEffect, useMemo } from 'react'
import type { RawMessageDTO } from '../api/types'
import { b64ToString, triggerDownload } from '../lib/download'

const VIEW_CAP_BYTES = 1024 * 1024 // 1 MiB

interface Props {
  dto: RawMessageDTO
  onClose: () => void
}

export default function RawMessageDialog({ dto, onClose }: Props) {
  // Decode once. atob is byte-exact so re-renders don't reshuffle.
  const decoded = useMemo(() => b64ToString(dto.raw_b64), [dto.raw_b64])
  const truncated = decoded.length > VIEW_CAP_BYTES
  const visible = truncated ? decoded.slice(0, VIEW_CAP_BYTES) : decoded

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  const onDownload = () => triggerDownload(dto.raw_b64, dto.filename)
  const onCopy = async () => {
    try { await navigator.clipboard.writeText(decoded) } catch { /* ignore */ }
  }

  return (
    <div className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4" role="dialog" aria-modal="true">
      <div className="bg-zinc-900 border border-zinc-800 rounded shadow-xl w-full max-w-4xl flex flex-col">
        <div className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
          <span className="font-mono text-sm text-zinc-300 truncate">{dto.filename}</span>
          <span className="text-xs text-zinc-500">{dto.size_bytes} bytes</span>
          <div className="flex-1" />
          <button type="button" onClick={onCopy} className="text-xs px-2 py-1 rounded border border-zinc-700 hover:bg-zinc-800">Copy</button>
          <button type="button" onClick={onDownload} className="text-xs px-2 py-1 rounded border border-zinc-700 hover:bg-zinc-800">Download .eml</button>
          <button type="button" onClick={onClose} aria-label="Close" className="text-xs px-2 py-1 rounded border border-zinc-700 hover:bg-zinc-800">Close</button>
        </div>
        {truncated && (
          <div className="bg-amber-950/50 text-amber-200 text-xs px-4 py-1.5 border-b border-zinc-800">
            Truncated to 1 MiB. Download the .eml to see the full message.
          </div>
        )}
        <pre className="font-mono text-xs whitespace-pre overflow-auto max-h-[70vh] p-4 text-zinc-200">
          {visible}
        </pre>
      </div>
    </div>
  )
}
