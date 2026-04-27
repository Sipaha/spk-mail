import { useEffect } from 'react'
import { client } from './client'
import { useStore } from '../store'

export function useEventStream() {
  useEffect(() => {
    return client.subscribeEvents(async (ev) => {
      const s = useStore.getState()
      switch (ev.type) {
        case 'MessageInserted':
        case 'MessageUpdated':
        case 'MessageArrived':
          s.setThreads(await client.listThreads({ account_id: s.filter.accountId, limit: 200 }))
          if (s.openThreadId !== undefined) {
            s.setOpenThread(s.openThreadId, await client.getThread(s.openThreadId))
          }
          break
        case 'AccountStatus': {
          const id = Number(ev.payload.account_id), state = String(ev.payload.state ?? 'ok')
          const list = await client.listAccounts()
          s.setAccounts(list.map(a => a.id === id ? { ...a, status: state } : a))
          break
        }
        case 'SyncProgress':
          s.setSyncProgress(Number(ev.payload.account_id), String(ev.payload.folder ?? ''), Number(ev.payload.done ?? 0), Number(ev.payload.total ?? 0))
          break
        case 'WriteError':
          // surface in console for now; toast UI is plan 7
          console.error('write error', ev.payload); break
      }
    })
  }, [])
}
