import { useState } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'
import AddAccount from './AddAccount'

export default function Settings() {
  const accounts = useStore(s => s.accounts)
  const setAccounts = useStore(s => s.setAccounts)
  const [adding, setAdding] = useState(false)

  const refresh = async () => setAccounts(await client.listAccounts())

  return (
    <div className="p-6 max-w-2xl space-y-4">
      <h1 className="text-xl font-semibold">Accounts</h1>
      <ul className="space-y-2">
        {accounts.map(a => (
          <li key={a.id} className="flex items-center gap-3 rounded border border-zinc-800 px-3 py-2">
            <span className="size-3 rounded-full" style={{ background: a.color }} />
            <div className="flex-1">
              <div className="font-medium">{a.name}</div>
              <div className="text-xs text-zinc-500">{a.email}</div>
            </div>
            <button onClick={async () => {
              if (!window.confirm(`Remove account "${a.name}" (${a.email})? This stops sync and deletes stored credentials.`)) return
              try {
                await client.removeAccount(a.id)
                await refresh()
              } catch (e) {
                const msg = e instanceof Error ? e.message : String(e)
                window.alert(`Failed to remove account: ${msg}`)
              }
            }}
              className="text-xs rounded border border-red-900/50 hover:bg-red-900/30 px-2 py-1 text-red-400">
              Remove
            </button>
          </li>
        ))}
      </ul>
      {!adding && <button onClick={() => setAdding(true)} className="rounded bg-blue-600 hover:bg-blue-500 px-3 py-1.5 text-sm">Add account</button>}
      {adding && <AddAccount onDone={() => { setAdding(false); refresh() }} />}
    </div>
  )
}
