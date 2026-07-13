import { useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import AddAccount from './AddAccount'
import NewProfileDialog from '../components/NewProfileDialog'
import { AlertIcon, BellIcon } from '../components/icons'

// Human wording for the transient connection states. `ok` renders nothing:
// ListAccounts returns a synthetic "ok" for every row (real status only
// exists on the event bus), so a green "Connected" badge here could
// overpromise.
const STATUS_LABEL: Record<string, string> = {
  connecting: 'Connecting…',
  starting: 'Starting…',
}

export default function Settings() {
  const accounts = useStore(s => s.accounts)
  const setAccounts = useStore(s => s.setAccounts)
  const profiles = useStore(s => s.profiles)
  const setProfiles = useStore(s => s.setProfiles)
  const activeProfileId = useStore(s => s.activeProfileId)
  const setActiveProfile = useStore(s => s.setActiveProfile)
  const accountDetail = useStore(s => s.accountDetail)
  const [adding, setAdding] = useState(false)
  const [creatingProfile, setCreatingProfile] = useState(false)

  const refresh = async () => setAccounts(await client.listAccounts())

  const profileName = (id?: number) =>
    id == null ? null : profiles.find(p => p.id === id)?.name ?? null

  const onDeleteProfile = async (id: number) => {
    const target = profiles.find(p => p.id === id)
    if (!target) return
    if (!window.confirm(`Delete profile "${target.name}"?`)) return
    try {
      await client.deleteProfile(id)
      const fresh = await client.listProfiles()
      setProfiles(fresh)
      if (activeProfileId === id) setActiveProfile(fresh[0]?.id ?? null)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (/attached accounts/i.test(msg)) {
        window.alert('This profile still has accounts attached. Move or remove them first.')
      } else {
        window.alert(`Delete failed: ${msg}`)
      }
    }
  }

  return (
    <div className="mx-auto max-w-2xl space-y-10 px-6 py-8">
      <section className="space-y-3">
        <div className="flex items-baseline justify-between">
          <h2 className="text-base font-semibold">Accounts</h2>
          <span className="font-mono text-[11px] text-fg-faint">{accounts.length} configured</span>
        </div>

        {accounts.length === 0 && (
          <p className="rounded-md border border-edge bg-ink-900 px-4 py-6 text-center text-[13px] text-fg-faint">
            No accounts yet. Add one below to start syncing mail.
          </p>
        )}

        <ul className="space-y-2">
          {accounts.map(a => {
            const isError = a.status === 'error'
            const transient = STATUS_LABEL[a.status]
            return (
              <li key={a.id} className="overflow-hidden rounded-md border border-edge bg-ink-900">
                <div className="flex items-center gap-3 px-4 py-3">
                  <span className="size-3 shrink-0 rounded-full" style={{ background: a.color }} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline gap-2">
                      <span className="truncate text-[13px] font-medium">{a.name}</span>
                      {profileName(a.profile_id) && (
                        <span className="shrink-0 rounded border border-edge-strong px-1.5 font-mono text-[10px] leading-relaxed text-fg-faint">
                          {profileName(a.profile_id)}
                        </span>
                      )}
                      {a.profile_id == null && (
                        <span className="shrink-0 rounded border border-warn/40 px-1.5 font-mono text-[10px] leading-relaxed text-warn" title="This account is not attached to any profile; it is shown in every profile.">
                          no profile
                        </span>
                      )}
                      {transient && <span className="shrink-0 font-mono text-[10px] text-warn">{transient}</span>}
                    </div>
                    <div className="truncate font-mono text-[11px] text-fg-faint">{a.email}</div>
                  </div>
                  <button
                    onClick={async () => {
                      if (!window.confirm(`Remove account "${a.name}" (${a.email})? This stops sync and deletes stored credentials.`)) return
                      try {
                        await client.removeAccount(a.id)
                        await refresh()
                      } catch (e) {
                        const msg = e instanceof Error ? e.message : String(e)
                        window.alert(`Failed to remove account: ${msg}`)
                      }
                    }}
                    className="shrink-0 rounded border border-danger/40 px-2.5 py-1 text-xs text-danger hover:bg-danger/10">
                    Remove
                  </button>
                </div>
                {isError && (
                  <div className="flex items-start gap-2 border-t border-edge bg-danger/5 px-4 py-2.5" role="status">
                    <AlertIcon className="mt-px size-3.5 shrink-0 text-danger" />
                    <div className="min-w-0 text-xs leading-relaxed">
                      <div className="font-medium text-danger">Can't connect to this account</div>
                      <div className="break-words font-mono text-[11px] text-fg-sub">{accountDetail[a.id] ?? 'Connection failed'}</div>
                      <div className="mt-0.5 text-fg-faint">
                        Sync retries automatically. Check the server address and password, or remove the account if it is no longer needed.
                      </div>
                    </div>
                  </div>
                )}
              </li>
            )
          })}
        </ul>

        {!adding && (
          <button
            onClick={() => setAdding(true)}
            className="rounded-md bg-accent-deep px-3 py-1.5 text-[13px] font-medium text-fg hover:bg-accent-deep/80">
            Add account
          </button>
        )}
        {adding && (
          <div className="rounded-md border border-edge bg-ink-900">
            <AddAccount onDone={() => { setAdding(false); refresh() }} onCancel={() => setAdding(false)} />
          </div>
        )}
      </section>

      <section className="space-y-3">
        <div className="flex items-baseline justify-between">
          <h2 className="text-base font-semibold">Profiles</h2>
          <span className="font-mono text-[11px] text-fg-faint">{profiles.length} configured</span>
        </div>
        <p className="text-xs leading-relaxed text-fg-faint">
          Profiles group accounts into separate workspaces — switch between them at the top of the sidebar.
          A profile can only be deleted after its accounts are removed.
        </p>
        <ul className="space-y-2">
          {profiles.map(p => {
            const attached = accounts.filter(a => a.profile_id === p.id).length
            return (
              <li key={p.id} className="flex items-center gap-3 rounded-md border border-edge bg-ink-900 px-4 py-2.5">
                <span className="size-3 shrink-0 rounded-full" style={{ background: p.color }} />
                <span className="truncate text-[13px] font-medium">{p.name}</span>
                <span className="font-mono text-[10px] text-fg-faint">
                  {attached === 1 ? '1 account' : `${attached} accounts`}
                </span>
                <span className="ml-auto flex items-center gap-1">
                  <button
                    title={p.muted ? 'Unmute notifications' : 'Mute notifications'}
                    aria-label={p.muted ? `Unmute profile ${p.name}` : `Mute profile ${p.name}`}
                    className={`rounded p-1 hover:bg-ink-800 ${p.muted ? 'text-warn' : 'text-fg-faint hover:text-fg'}`}
                    onClick={async () => {
                      await client.setProfileMuted(p.id, !p.muted)
                      setProfiles(await client.listProfiles())
                    }}>
                    <BellIcon slashed={p.muted} className="size-3.5" />
                  </button>
                  <button
                    onClick={() => onDeleteProfile(p.id)}
                    className="rounded border border-danger/40 px-2.5 py-1 text-xs text-danger hover:bg-danger/10">
                    Delete
                  </button>
                </span>
              </li>
            )
          })}
        </ul>
        <button
          onClick={() => setCreatingProfile(true)}
          className="rounded-md border border-edge-strong px-3 py-1.5 text-[13px] text-fg-sub hover:bg-ink-800 hover:text-fg">
          New profile
        </button>
        {creatingProfile && (
          <NewProfileDialog
            onDone={async () => { setCreatingProfile(false); setProfiles(await client.listProfiles()) }}
            onCancel={() => setCreatingProfile(false)}
          />
        )}
      </section>
    </div>
  )
}
