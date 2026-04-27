import { useEffect, useState } from 'react'
import { client } from './api/client'

export default function App() {
  const [accounts, setAccounts] = useState<unknown[]>([])
  useEffect(() => { client.listAccounts().then(setAccounts).catch(() => setAccounts([])) }, [])
  return (
    <div className="min-h-screen p-6">
      <h1 className="text-2xl font-semibold">spk-mail</h1>
      <p className="text-sm text-zinc-400 mt-1">Accounts: {accounts.length}</p>
      <p className="text-xs text-zinc-500 mt-4">Plan 1 shell. Plans 2–7 add the real UI.</p>
    </div>
  )
}
