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
      className="block w-full text-left px-3 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
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
        className="text-zinc-500 hover:text-zinc-200 px-1"
      >
        ⋯
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 w-44 rounded border border-zinc-800 bg-zinc-900 shadow-lg z-30">
          {item('View raw…', onViewRaw)}
          {item('Download .eml', onDownloadEml)}
        </div>
      )}
    </div>
  )
}
