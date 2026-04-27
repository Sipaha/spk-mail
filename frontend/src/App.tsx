import { useEffect, useState } from 'react'
import { client } from './api/client'
import { useEventStream } from './api/events'
import { useStore } from './store'
import Layout from './components/Layout'
import AccountSidebar from './components/AccountSidebar'
import ThreadList from './components/ThreadList'
import ThreadView from './components/ThreadView'
import Settings from './pages/Settings'

export default function App() {
  const [route, setRoute] = useState<string>(window.location.hash || '#/')
  const setAccounts = useStore(s => s.setAccounts)
  useEffect(() => {
    const onHash = () => setRoute(window.location.hash || '#/')
    window.addEventListener('hashchange', onHash); return () => window.removeEventListener('hashchange', onHash)
  }, [])
  useEffect(() => { client.listAccounts().then(setAccounts) }, [setAccounts])
  useEventStream()

  if (route.startsWith('#/settings')) {
    return <div className="bg-zinc-950 text-zinc-100 min-h-screen"><a href="#/" className="absolute top-3 left-3 text-xs text-zinc-500">← back</a><Settings /></div>
  }

  return (
    <Layout
      sidebar={<AccountSidebar />}
      list={<ThreadList />}
      detail={<ThreadView />}
    />
  )
}
