import { useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'

const palette = ['#3b82f6','#10b981','#f59e0b','#ef4444','#a855f7','#ec4899']

export default function AddAccount({ onDone, onCancel }: { onDone: () => void; onCancel?: () => void }) {
  const [form, setForm] = useState({ name: '', email: '', imap_host: '', imap_port: 993, imap_username: '', imap_password: '', use_tls: true, color: palette[0] })
  const [err, setErr] = useState<string>()
  const [busy, setBusy] = useState(false)
  const upsert = useStore(s => s.upsertAccount)
  const activeProfileId = useStore(s => s.activeProfileId)
  const profiles = useStore(s => s.profiles)
  const profileName = profiles.find(p => p.id === activeProfileId)?.name

  return (
    <form className="mx-auto max-w-md space-y-5 p-6 text-sm"
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
      <div>
        <h2 className="text-base font-semibold">Add IMAP account</h2>
        <p className="mt-1 text-xs leading-relaxed text-fg-faint">
          {profileName
            ? <>The account will be added to the <span className="text-fg-sub">{profileName}</span> profile.</>
            : 'The account will be visible in every profile.'}
        </p>
      </div>

      <fieldset className="space-y-3">
        <legend className="mb-1 font-mono text-[11px] uppercase tracking-wider text-fg-faint">Identity</legend>
        <Field label="Display name" value={form.name} onChange={v => setForm({ ...form, name: v })} required />
        <Field label="Email"        value={form.email} onChange={v => setForm({ ...form, email: v, imap_username: form.imap_username || v })} type="email" required />
        <div className="flex items-center gap-2">
          <span className="text-xs text-fg-sub">Color</span>
          {palette.map(c => (
            <button key={c} type="button" onClick={() => setForm({ ...form, color: c })}
              aria-label={`Color ${c}${form.color === c ? ' (selected)' : ''}`}
              aria-pressed={form.color === c}
              className="size-6 rounded-full border-2"
              style={{ background: c, borderColor: form.color === c ? '#fff' : 'transparent' }} />
          ))}
        </div>
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="mb-1 font-mono text-[11px] uppercase tracking-wider text-fg-faint">IMAP server</legend>
        <div className="grid grid-cols-[1fr_7rem] gap-3">
          <Field label="IMAP host" value={form.imap_host} onChange={v => setForm({ ...form, imap_host: v })} required mono />
          <Field label="IMAP port" value={String(form.imap_port)} onChange={v => setForm({ ...form, imap_port: Number(v) || 993 })} type="number" mono />
        </div>
        <label className="flex items-center gap-2 text-[13px] text-fg-sub">
          <input type="checkbox" checked={form.use_tls} onChange={e => setForm({ ...form, use_tls: e.target.checked })} />
          Use TLS
        </label>
        <Field label="Username" value={form.imap_username} onChange={v => setForm({ ...form, imap_username: v })} required mono />
        <Field label="App password" value={form.imap_password} onChange={v => setForm({ ...form, imap_password: v })} type="password" required />
        <p className="text-[11px] leading-relaxed text-fg-faint">
          Most providers require an app-specific password for IMAP — your normal
          sign-in password usually won't work. The password is stored in the
          system keyring.
        </p>
      </fieldset>

      {err && <div role="alert" className="rounded-md border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">{err}</div>}

      <div className="flex items-center gap-2">
        <button disabled={busy} className="rounded-md bg-accent-deep px-3 py-1.5 text-[13px] font-medium text-fg hover:bg-accent-deep/80 disabled:opacity-50">
          {busy ? 'Adding…' : 'Add account'}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className="rounded-md px-3 py-1.5 text-[13px] text-fg-sub hover:bg-ink-800 hover:text-fg">
            Cancel
          </button>
        )}
      </div>
    </form>
  )
}

function Field({ label, value, onChange, type = 'text', required = false, mono = false }: { label: string; value: string; onChange: (v: string) => void; type?: string; required?: boolean; mono?: boolean }) {
  return (
    <label className="block">
      <div className="mb-1 text-xs text-fg-sub">{label}</div>
      <input className={`w-full rounded-md border border-edge-strong bg-ink-850 px-2.5 py-1.5 text-[13px] focus:border-accent/60 ${mono ? 'font-mono' : ''}`}
        type={type} value={value} required={required} onChange={e => onChange(e.target.value)} />
    </label>
  )
}
