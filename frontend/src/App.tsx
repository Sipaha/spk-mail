import { useEffect, useRef, useState } from 'react'
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

// useBootstrap fetches the account + profile lists once on app start (and
// again on demand via `retry`). This must run for EVERY route — including
// `#/settings` and `#/add-account`, which return early from App() before the
// main-layout tree mounts — because Settings.tsx renders straight from the
// store's `accounts` with no mount-time fetch of its own. Keeping the effect
// here (owned by App, not by the presentational AppBanners) is what makes
// that guarantee hold regardless of which branch App() returns.
function useBootstrap() {
  const bootstrapError = useStore(s => s.bootstrapError)
  const setBootstrapError = useStore(s => s.setBootstrapError)
  const setAccounts = useStore(s => s.setAccounts)
  const setProfiles = useStore(s => s.setProfiles)
  const [retryKey, setRetryKey] = useState(0)

  useEffect(() => {
    setBootstrapError(null)
    Promise.all([client.listAccounts(), client.listProfiles()])
      .then(([accounts, profiles]) => {
        setAccounts(accounts)
        setProfiles(profiles)
      })
      .catch(err => {
        console.warn('bootstrap failed', err)
        setBootstrapError(err instanceof Error ? err.message : String(err))
      })
  }, [setAccounts, setProfiles, setBootstrapError, retryKey])

  return { bootstrapError, retryBootstrap: () => setRetryKey(k => k + 1) }
}

// Presentational: renders the bootstrap-failure / sync-error banners. Takes
// bootstrapError + retry handler as props so every route (main layout,
// settings, add-account) can share the single bootstrap fetch owned by App()
// while still surfacing (and letting the user retry) a failure.
function AppBanners({ bootstrapError, onRetryBootstrap }: { bootstrapError: string | null; onRetryBootstrap: () => void }) {
  const writeError = useStore(s => s.writeError)
  const setWriteError = useStore(s => s.setWriteError)

  if (!bootstrapError && !writeError) return null

  return (
    <div className="border-b border-zinc-800 bg-zinc-900 text-sm">
      {bootstrapError && (
        <div className="px-4 py-2 text-rose-400 flex items-center gap-2" role="alert">
          <span>Failed to load accounts and profiles: {bootstrapError}</span>
          <button
            type="button"
            onClick={onRetryBootstrap}
            className="text-blue-400 hover:text-blue-300 underline shrink-0"
          >
            Retry
          </button>
        </div>
      )}
      {writeError && (
        <div className="px-4 py-2 text-amber-400 flex items-center gap-2" role="alert">
          <span>Sync error: {writeError}</span>
          <button
            type="button"
            onClick={() => setWriteError(null)}
            className="text-zinc-400 hover:text-zinc-200 underline shrink-0 ml-auto"
          >
            Dismiss
          </button>
        </div>
      )}
    </div>
  )
}

export default function App() {
  const [route, setRoute] = useState<string>(window.location.hash || '#/')
  const setOpenThread = useStore(s => s.setOpenThread)
  const setSearch = useStore(s => s.setSearch)
  const { bootstrapError, retryBootstrap } = useBootstrap()
  const openedDeepLink = useRef<number | null>(null)
  useEffect(() => {
    const onHash = () => setRoute(window.location.hash || '#/')
    window.addEventListener('hashchange', onHash); return () => window.removeEventListener('hashchange', onHash)
  }, [])
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
  //
  // openedDeepLink dedupes by id. React StrictMode invokes an effect twice on
  // mount with the SAME render's `route`, so without a guard the second run
  // would fire a second getThread + markRead for the same notification. A ref
  // is enough — StrictMode's simulated remount preserves refs and the Zustand
  // store, and the first run's in-flight fetch still lands, so nothing has to
  // survive in sessionStorage.
  //
  // Mark-read mirrors ThreadList.onOpen — clicking a notification is an
  // explicit user action, so we don't gate on document.hasFocus() the
  // way the SSE auto-mark-read does (the webview may not have flagged
  // focus yet by the time getThread resolves, and the user's intent is
  // unambiguous).
  useEffect(() => {
    const fromHash = route.match(/^#\/thread\/(\d+)/)
    if (!fromHash) return
    const id = parseInt(fromHash[1], 10)
    if (!Number.isFinite(id)) return
    if (openedDeepLink.current === id) return
    openedDeepLink.current = id

    setOpenThread(id, undefined)
    setRoute('#/')
    try { window.history.replaceState(null, '', '#/') } catch { /* ignore */ }
    client.getThread(id)
      .then(msgs => {
        if (useStore.getState().openThreadId !== id) return
        setOpenThread(id, msgs)
        const unread = msgs.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
        if (unread.length) {
          client.markRead(unread)
            .then(() => useStore.getState().markThreadRead(id))
            .catch(err => console.warn('deep-link markRead failed', err))
        }
      })
      .catch(err => console.warn('deep-link getThread failed', err))
  }, [route, setOpenThread])

  if (route.startsWith('#/add-account')) {
    return (
      <div className="bg-zinc-950 text-zinc-100 min-h-screen">
        <AppBanners bootstrapError={bootstrapError} onRetryBootstrap={retryBootstrap} />
        <a href="#/" className="absolute top-3 left-3 text-xs text-zinc-500">← back</a>
        <AddAccount onDone={() => { window.location.hash = '#/' }} />
      </div>
    )
  }

  if (route.startsWith('#/settings')) {
    return (
      <div className="bg-zinc-950 text-zinc-100 min-h-screen">
        <AppBanners bootstrapError={bootstrapError} onRetryBootstrap={retryBootstrap} />
        <a href="#/" className="absolute top-3 left-3 text-xs text-zinc-500">← back</a>
        <Settings />
      </div>
    )
  }

  const sidebar = <><ProfileSwitcher /><AccountSidebar /></>

  return (
    <div className="grid h-screen w-screen grid-rows-[auto_1fr] bg-zinc-950 text-zinc-100">
      <AppBanners bootstrapError={bootstrapError} onRetryBootstrap={retryBootstrap} />
      <Layout
        sidebar={sidebar}
        list={<MailListPane />}
        detail={<ThreadView />}
      />
    </div>
  )
}