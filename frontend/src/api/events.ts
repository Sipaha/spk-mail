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
        case 'MessageArrived': {
          const accId = Number(ev.payload.account_id)
          if (Number.isFinite(accId) && accId > 0) {
            client.listFolders(accId).then(fs => useStore.getState().setFolders(accId, fs))
          }
          s.setThreads(await client.listThreads({ account_id: s.filter.accountId, limit: 200 }))
          if (s.openThreadId !== undefined) {
            s.setOpenThread(s.openThreadId, await client.getThread(s.openThreadId))
          }
          break
        }
        case 'AccountStatus': {
          const id = Number(ev.payload.account_id), state = String(ev.payload.state ?? 'ok')
          const list = await client.listAccounts()
          s.setAccounts(list.map(a => a.id === id ? { ...a, status: state } : a))
          break
        }
        case 'SyncProgress': {
          const folderId = ev.payload.folder_id != null ? Number(ev.payload.folder_id) : undefined
          s.setSyncProgress(
            Number(ev.payload.account_id),
            String(ev.payload.folder ?? ''),
            Number(ev.payload.done ?? 0),
            Number(ev.payload.total ?? 0),
            folderId,
          )
          break
        }
        case 'WriteError':
          // surface in console for now; toast UI is plan 7
          console.error('write error', ev.payload); break
        case 'AttachmentReady':
          if (s.openThreadId !== undefined) {
            s.setOpenThread(s.openThreadId, await client.getThread(s.openThreadId))
          }
          break
      }
    })
  }, [])
}
