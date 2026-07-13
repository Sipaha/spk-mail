import { useEffect, useId, useRef, useState } from 'react'
import { client } from '../api/client'

const palette = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#a855f7', '#ec4899']

export default function NewProfileDialog({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [color, setColor] = useState(palette[0])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()
  const titleId = useId()
  const dialogRef = useRef<HTMLDivElement>(null)

  // Restore focus to the previously-focused element when the dialog closes —
  // standard dialog UX so keyboard users don't lose their place.
  useEffect(() => {
    const previouslyFocused = document.activeElement as HTMLElement | null
    return () => previouslyFocused?.focus?.()
  }, [])

  // Esc closes the dialog. Tab focus-trap: cycle inside the dialog instead
  // of escaping to elements behind the modal.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCancel()
        return
      }
      if (e.key !== 'Tab' || !dialogRef.current) return
      const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), textarea, select, [tabindex]:not([tabindex="-1"])'
      )
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onCancel])

  return (
    <div
      ref={dialogRef}
      role="dialog"
      aria-modal="true"
      aria-labelledby={titleId}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <form
        className="bg-ink-900 border border-edge-strong rounded-md p-4 w-72 space-y-3 text-sm shadow-xl"
        onSubmit={async e => {
          e.preventDefault()
          setBusy(true); setErr(undefined)
          try { await client.addProfile({ name: name.trim(), color }); onDone() }
          catch (e) { setErr(String(e)) }
          finally { setBusy(false) }
        }}>
        <h3 id={titleId} className="text-base font-semibold">New profile</h3>
        <label className="block">
          <div className="text-xs text-fg-sub mb-1">Name</div>
          <input autoFocus required value={name} onChange={e => setName(e.target.value)}
            className="w-full rounded-md bg-ink-850 border border-edge-strong focus:border-accent/60 px-2.5 py-1.5 text-[13px]" />
        </label>
        <div className="flex gap-2 items-center">
          <span className="text-xs text-fg-sub">Color:</span>
          {palette.map(c => (
            <button key={c} type="button" onClick={() => setColor(c)}
              aria-label={`Color ${c}${color === c ? ' (selected)' : ''}`}
              aria-pressed={color === c}
              className="size-6 rounded-full border-2"
              style={{ background: c, borderColor: color === c ? '#fff' : 'transparent' }} />
          ))}
        </div>
        {err && <div role="alert" className="text-danger text-xs">{err}</div>}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={onCancel} className="px-2 py-1 text-fg-sub hover:text-fg">Cancel</button>
          <button type="submit" disabled={busy || !name.trim()} className="rounded-md bg-accent-deep hover:bg-accent-deep/80 disabled:opacity-50 px-3 py-1.5 text-[13px] font-medium">
            {busy ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  )
}
