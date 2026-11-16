import { useEffect } from 'react'
import { client } from './client'
import { useStore } from '../store'
import { filterSig, scopeThreadMessages } from '../lib/filterSig'
import { refetchLimit } from '../lib/paging'

export function useEventStream() {
  useEffect(() => {
    let disposed = false
    let refreshRunning = false
    let refreshPending = false
    let activeRefresh: Promise<void> | undefined
    const pendingAccounts = new Set<number>()
    let accountStatusSeq = 0

    const performRefresh = async (accIds: number[]) => {
      for (const accId of accIds) {
        client.listFolders(accId)
          .then(fs => {
            if (!disposed) useStore.getState().setFolders(accId, fs)
          })
          .catch(err => console.warn('listFolders refresh failed', err))
      }

      const s = useStore.getState()
      const reqFilter = s.filter
      const reqProfileId = s.activeProfileId
      const reqSig = filterSig(reqFilter, reqProfileId)
      const result = await client.listThreads({
        account_id: reqFilter.accountId,
        folder_id: reqFilter.folderId,
        unread_only: reqFilter.unreadOnly,
        has_flagged: reqFilter.hasFlagged,
        profile_id: reqProfileId ?? undefined,
        limit: refetchLimit(s.threads.length),
      })
      if (disposed) return
      const now = useStore.getState()
      if (filterSig(now.filter, now.activeProfileId) === reqSig) {
        now.setThreads(result)
      }

      // openThreadId is captured AFTER the listThreads await using a fresh
      // state read and re-checked after getThread resolves so closing or
      // switching threads during the await never leaks stale messages.
      const after = useStore.getState()
      if (after.openThreadId !== undefined) {
        const id = after.openThreadId
        const msgs = await client.getThread(id)
        if (disposed) return
        const final = useStore.getState()
        if (final.openThreadId === id) {
          final.setOpenThread(id, msgs)
          if (typeof document !== 'undefined' && document.hasFocus()) {
            const scoped = scopeThreadMessages(msgs, final.filter.folderId)
            const unread = scoped.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
            if (unread.length) {
              client.markRead(unread)
                .then(() => useStore.getState().markThreadRead(id))
                .catch(err => console.warn('auto markRead failed', err))
            }
          }
        }
      }
    }

    const scheduleRefresh = (accId?: number): Promise<void> => {
      if (accId !== undefined && Number.isFinite(accId) && accId > 0) {
        pendingAccounts.add(accId)
      }
      if (refreshRunning) {
        refreshPending = true
        return activeRefresh ?? Promise.resolve()
      }
      refreshRunning = true
      activeRefresh = (async () => {
        try {
          do {
            refreshPending = false
            const accIds = Array.from(pendingAccounts)
            pendingAccounts.clear()
            try {
              await performRefresh(accIds)
            } catch (err) {
              console.warn('event refresh failed', err)
            }
          } while (refreshPending && !disposed)
        } finally {
          refreshRunning = false
          activeRefresh = undefined
        }
      })()
      return activeRefresh
    }

    const unsub = client.subscribeEvents(async (ev) => {
      switch (ev.type) {
        case 'MessageInserted':
        case 'MessageUpdated':
        case 'MessageArrived': {
          const accId = Number(ev.payload.account_id)
          await scheduleRefresh(accId)
          break
        }
        case 'Reconcile': {
          const seq = ++accountStatusSeq
          if (typeof client.listAccounts === 'function') {
            client.listAccounts()
              .then(list => {
                if (disposed || seq !== accountStatusSeq) return
                useStore.getState().setAccounts(list)
                for (const a of list) {
                  useStore.getState().setAccountDetail(a.id, a.status === 'error' ? (a.detail ?? 'Connection failed') : undefined)
                  pendingAccounts.add(a.id)
                }
              })
              .catch(err => console.warn('reconcile listAccounts failed', err))
          }
          for (const a of useStore.getState().accounts) {
            pendingAccounts.add(a.id)
          }
          await scheduleRefresh()
          break
        }
        case 'AccountStatus': {
          const id = Number(ev.payload.account_id)
          const state = String(ev.payload.state ?? 'ok')
          const detail = ev.payload.detail != null ? String(ev.payload.detail) : undefined
          const errDetail = state === 'error' ? (detail ?? 'Connection failed') : undefined
          const st = useStore.getState()
          st.setAccountDetail(id, errDetail)
          if (st.accounts.some(a => a.id === id)) {
            st.setAccounts(st.accounts.map(a =>
              a.id === id ? { ...a, status: state, detail: errDetail } : a,
            ))
          }
          const seq = ++accountStatusSeq
          if (typeof client.listAccounts === 'function') {
            try {
              const list = await client.listAccounts()
              if (disposed || seq !== accountStatusSeq) break
              const latest = useStore.getState().accounts
              const prevById = new Map(latest.map(a => [a.id, a]))
              useStore.getState().setAccounts(list.map(a => {
                if (a.id === id) return { ...a, status: state, detail: errDetail }
                const cur = prevById.get(a.id)
                return cur ? { ...a, status: cur.status, detail: cur.detail ?? a.detail } : a
              }))
            } catch (err) {
              console.warn('accountStatus listAccounts failed', err)
            }
          }
          break
        }
        case 'SyncProgress': {
          const folderId = ev.payload.folder_id != null ? Number(ev.payload.folder_id) : undefined
          useStore.getState().setSyncProgress(
            Number(ev.payload.account_id),
            String(ev.payload.folder ?? ''),
            Number(ev.payload.done ?? 0),
            Number(ev.payload.total ?? 0),
            folderId,
          )
          break
        }
        case 'FolderMarkedRead': {
          const accId = Number(ev.payload.account_id)
          const folderId = Number(ev.payload.folder_id)
          if (Number.isFinite(accId) && accId > 0) {
            client.listFolders(accId)
              .then(fs => {
                if (!disposed) useStore.getState().setFolders(accId, fs)
              })
              .catch(err => console.warn('listFolders refresh failed', err))
          }
          const s = useStore.getState()
          const reqFilter = s.filter
          const accountMismatches = reqFilter.accountId !== undefined && reqFilter.accountId !== accId
          const folderMismatches = reqFilter.folderId !== undefined && reqFilter.folderId !== folderId
          if (accountMismatches || folderMismatches) {
            break
          }
          const reqProfileId = s.activeProfileId
          const reqSig = filterSig(reqFilter, reqProfileId)
          const result = await client.listThreads({
            account_id: reqFilter.accountId,
            folder_id: reqFilter.folderId,
            unread_only: reqFilter.unreadOnly,
            has_flagged: reqFilter.hasFlagged,
            profile_id: reqProfileId ?? undefined,
            limit: refetchLimit(s.threads.length),
          })
          if (disposed) break
          const now = useStore.getState()
          if (filterSig(now.filter, now.activeProfileId) === reqSig) {
            now.setThreads(result)
          }
          const after = useStore.getState()
          if (after.openThreadId !== undefined) {
            const id = after.openThreadId
            const msgs = await client.getThread(id)
            if (disposed) break
            const final = useStore.getState()
            if (final.openThreadId === id) {
              final.setOpenThread(id, msgs)
            }
          }
          break
        }
        case 'WriteError': {
          const msg = String(ev.payload.err ?? 'Write failed')
          console.error('write error', ev.payload)
          useStore.getState().setWriteError(msg)
          break
        }
        case 'AttachmentReady': {
          const fresh = useStore.getState()
          if (fresh.openThreadId === undefined) break
          const msgId = ev.payload && (ev.payload as { message_id?: unknown }).message_id
          const msgIdNum = typeof msgId === 'number' ? msgId : Number(msgId)
          if (Number.isFinite(msgIdNum) && fresh.openThread) {
            const inThread = fresh.openThread.some(m => m.id === msgIdNum)
            if (!inThread) break
          }
          const id = fresh.openThreadId
          const msgs = await client.getThread(id)
          if (disposed) break
          const final = useStore.getState()
          if (final.openThreadId === id) {
            final.setOpenThread(id, msgs)
          }
          break
        }
      }
    })
    return () => {
      disposed = true
      unsub()
    }
  }, [])

  useEffect(() => {
    const onFocus = () => {
      const s = useStore.getState()
      const id = s.openThreadId
      if (id === undefined || !s.openThread) return
      const scoped = scopeThreadMessages(s.openThread, s.filter.folderId)
      const unread = scoped.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
      if (unread.length === 0) return
      client.markRead(unread)
        .then(() => useStore.getState().markThreadRead(id))
        .catch(err => console.warn('focus markRead failed', err))
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [])
}
