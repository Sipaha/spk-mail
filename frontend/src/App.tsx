import { useEffect, useState } from 'react'
import { client } from './api/client'
import { useEventStream } from './api/events'
import { useStore } from './store'
import Layout from './components/Layout'
import AccountSidebar from './components/AccountSidebar'
import ProfileSwitcher from './components/ProfileSwitcher'
import MailListPane from './components/MailListPane'
import ThreadView from './components/ThreadView'
import Settings from './pages/Settings'
import AddAccount from './pages/AddAccount'

export default function App() {
  const [route, setRoute] = useState<string>(window.location.hash || '#/')
  const setAccounts = useStore(s => s.setAccounts)
  const setOpenThread = useStore(s => s.setOpenThread)
  const setSearch = useStore(s => s.setSearch)
  useEffect(() => {
    const onHash = () => setRoute(window.location.hash || '#/')
    window.addEventListener('hashchange', onHash); return () => window.removeEventListener('hashchange', onHash)
  }, [])
  useEffect(() => {
    client.listAccounts()
      .then(setAccounts)
      .catch(err => console.warn('listAccounts failed', err))
  }, [setAccounts])
  useEventStream()

  // Legacy deep link: #/search?q=foo predates the in-pane search input. Seed
  // the store search field from the URL, then redirect to "#/" so the search
  // UX is the only visible affordance going forward.
  useEffect(() => {
    const m = route.match(/^#\/search\?q=(.*)$/)
    if (!m) return
    setSearch(decodeURIComponent(m[1]))
    window.location.hash = '#/'
  }, [route, setSearch])

  // Deep-link route: the desktop entry point used by the system-tray
  // notification click. The Go side calls SetURL("/#/thread/<id>") on
  // ActionInvoked; here we read the id, mark the thread as opening so
  // ThreadView shows "Loading…" immediately, navigate back to "#/" so
  // the URL doesn't keep firing this branch on subsequent renders, and
  // load the thread asynchronously. The post-resolve openThreadId
  // check mirrors events.ts: if the user navigated away during the
  // fetch, don't clobber their state.
  useEffect(() => {
    const m = route.match(/^#\/thread\/(\d+)/)
    if (!m) return
    const id = parseInt(m[1], 10)
    if (!Number.isFinite(id)) return
    setOpenThread(id, undefined)
    window.location.hash = '#/'
    client.getThread(id)
      .then(msgs => {
        if (useStore.getState().openThreadId === id) {
          setOpenThread(id, msgs)
        }
      })
      .catch(err => console.warn('deep-link getThread failed', err))
  }, [route, setOpenThread])

  if (route.startsWith('#/add-account')) {
    return (
      <div className="bg-zinc-950 text-zinc-100 min-h-screen">
        <a href="#/" className="absolute top-3 left-3 text-xs text-zinc-500">← back</a>
        <AddAccount onDone={() => { window.location.hash = '#/' }} />
      </div>
    )
  }

  if (route.startsWith('#/settings')) {
    return <div className="bg-zinc-950 text-zinc-100 min-h-screen"><a href="#/" className="absolute top-3 left-3 text-xs text-zinc-500">← back</a><Settings /></div>
  }

  const sidebar = <><ProfileSwitcher /><AccountSidebar /></>

  return (
    <Layout
      sidebar={sidebar}
      list={<MailListPane />}
      detail={<ThreadView />}
    />
  )
}
