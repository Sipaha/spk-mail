import { useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'

const palette = ['#3b82f6','#10b981','#f59e0b','#ef4444','#a855f7','#ec4899']

export default function AddAccount({ onDone }: { onDone: () => void }) {
  const [form, setForm] = useState({ name: '', email: '', imap_host: '', imap_port: 993, imap_username: '', imap_password: '', use_tls: true, color: palette[0] })
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const upsert = useStore(s => s.upsertAccount)
  const activeProfileId = useStore(s => s.activeProfileId)

  return (
    <form className="max-w-md p-6 space-y-3 text-sm"
      onSubmit={async e => {
        e.preventDefault(); setBusy(true); setErr(undefined)
        try {
          const payload = { ...form, profile_id: activeProfileId ?? undefined }
          const a = await client.addAccount(payload)
          upsert(a); onDone()
        } catch (e) {
          const msg = e instanceof Error ? e.message : String(e)
          setErr(msg || 'Unknown error')
        } finally { setBusy(false) }
      }}>
      <h2 className="text-lg font-semibold">Add IMAP account</h2>
      <Field label="Display name" value={form.name} onChange={v => setForm({ ...form, name: v })} required />
      <Field label="Email"        value={form.email} onChange={v => setForm({ ...form, email: v, imap_username: form.imap_username || v })} type="email" required />
      <Field label="IMAP host"    value={form.imap_host} onChange={v => setForm({ ...form, imap_host: v })} required />
      <Field label="IMAP port"    value={String(form.imap_port)} onChange={v => setForm({ ...form, imap_port: Number(v) || 993 })} type="number" />
      <Field label="Username"     value={form.imap_username} onChange={v => setForm({ ...form, imap_username: v })} required />
      <Field label="App password" value={form.imap_password} onChange={v => setForm({ ...form, imap_password: v })} type="password" required />
      <label className="flex items-center gap-2"><input type="checkbox" checked={form.use_tls} onChange={e => setForm({ ...form, use_tls: e.target.checked })} /> Use TLS</label>
      <div className="flex gap-2 items-center">
        <span>Color:</span>
        {palette.map(c => (
          <button key={c} type="button" onClick={() => setForm({ ...form, color: c })}
            className="size-6 rounded-full border-2"
            style={{ background: c, borderColor: form.color === c ? '#fff' : 'transparent' }} />
        ))}
      </div>
      {err && <div className="text-red-400">{err}</div>}
      <button disabled={busy} className="rounded bg-blue-600 hover:bg-blue-500 disabled:opacity-50 px-3 py-1.5">
        {busy ? 'Adding…' : 'Add account'}
      </button>
    </form>
  )
}

function Field({ label, value, onChange, type = 'text', required = false }: { label: string; value: string; onChange: (v: string) => void; type?: string; required?: boolean }) {
  return (
    <label className="block">
      <div className="text-zinc-400 text-xs">{label}</div>
      <input className="w-full rounded bg-zinc-900 border border-zinc-700 focus:border-zinc-500 px-2 py-1.5"
        type={type} value={value} required={required} onChange={e => onChange(e.target.value)} />
    </label>
  )
}
