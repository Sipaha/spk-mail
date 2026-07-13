import { useEffect, useRef, useState } from 'react'

interface Props {
  onViewRaw: () => void
  onDownloadEml: () => void
}

export default function MessageMenu({ onViewRaw, onDownloadEml }: Props) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onMouseDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onMouseDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onMouseDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const item = (label: string, onClick: () => void) => (
    <button
      type="button"
      onClick={() => { setOpen(false); onClick() }}
      className="block w-full text-left px-3 py-1.5 text-xs text-fg-sub hover:bg-ink-800 hover:text-fg"
    >
      {label}
    </button>
  )

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label="Message actions"
        onClick={() => setOpen(v => !v)}
        className="text-fg-faint hover:text-fg px-1"
      >
        ⋯
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-44 rounded-md border border-edge-strong bg-ink-850 shadow-lg z-30 py-1">
          {item('View raw…', onViewRaw)}
          {item('Download .eml', onDownloadEml)}
        </div>
      )}
    </div>
  )
}
