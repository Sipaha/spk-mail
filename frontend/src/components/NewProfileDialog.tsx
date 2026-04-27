import { useState } from 'react'
import { client } from '../api/client'

const palette = ['#3b82f6', '#10b981', '#f59e0b', '#ef4444', '#a855f7', '#ec4899']

export default function NewProfileDialog({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState('')
  const [color, setColor] = useState(palette[0])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()

  return (
    <div className="absolute inset-0 z-50 flex items-center justify-center bg-black/40">
      <form
        className="bg-zinc-900 border border-zinc-700 rounded p-4 w-72 space-y-3 text-sm"
        onSubmit={async e => {
          e.preventDefault()
          setBusy(true); setErr(undefined)
          try { await client.addProfile({ name: name.trim(), color }); onDone() }
          catch (e) { setErr(String(e)) }
          finally { setBusy(false) }
        }}>
        <h3 className="text-base font-semibold">New profile</h3>
        <label className="block">
          <div className="text-xs text-zinc-400">Name</div>
          <input autoFocus required value={name} onChange={e => setName(e.target.value)}
            className="w-full rounded bg-zinc-800 border border-zinc-700 focus:border-zinc-500 px-2 py-1.5" />
        </label>
        <div className="flex gap-2 items-center">
          <span className="text-xs text-zinc-400">Color:</span>
          {palette.map(c => (
            <button key={c} type="button" onClick={() => setColor(c)}
              className="size-6 rounded-full border-2"
              style={{ background: c, borderColor: color === c ? '#fff' : 'transparent' }} />
          ))}
        </div>
        {err && <div className="text-red-400 text-xs">{err}</div>}
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={onCancel} className="px-2 py-1 text-zinc-400 hover:text-zinc-200">Cancel</button>
          <button type="submit" disabled={busy || !name.trim()} className="rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50 px-3 py-1.5">
            {busy ? 'Creating…' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  )
}
