import { useEffect } from 'react'
import { client } from './client'
import { useStore } from '../store'
import { filterSig } from '../lib/filterSig'
import { refetchLimit } from '../lib/paging'

export function useEventStream() {
  useEffect(() => {
    return client.subscribeEvents(async (ev) => {
      switch (ev.type) {
        case 'MessageInserted':
        case 'MessageUpdated':
        case 'MessageArrived': {
          const s = useStore.getState()
          const accId = Number(ev.payload.account_id)
          if (Number.isFinite(accId) && accId > 0) {
            client.listFolders(accId)
              .then(fs => useStore.getState().setFolders(accId, fs))
              .catch(err => console.warn('listFolders refresh failed', err))
          }
          // Refetch using the FULL active filter, not just account_id —
          // otherwise an Unread / Flagged / per-folder view would silently
          // get replaced by the unfiltered list on every Mark-Read event.
          // Snapshot the filter signature; after the await, drop the result
          // if the user switched profile/folder during the fetch — ThreadList's
          // own effect is already refetching for the new scope, so applying
          // the stale-scope result here would just cause a visible flash.
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
          const now = useStore.getState()
          if (filterSig(now.filter, now.activeProfileId) === reqSig) {
            now.setThreads(result)
          }
          // openThreadId is captured AFTER the listThreads await using a
          // fresh state read: if the user closed the thread mid-await, the
          // closed state must win — the snapshot taken at handler entry
          // would otherwise re-open a thread the user just dismissed.
          // We re-check ONCE MORE after getThread resolves: closing the
          // thread during getThread should also win, and opening a different
          // thread mid-await means the response we got is for the wrong id.
          const after = useStore.getState()
          if (after.openThreadId !== undefined) {
            const id = after.openThreadId
            const msgs = await client.getThread(id)
            const final = useStore.getState()
            if (final.openThreadId === id) {
              final.setOpenThread(id, msgs)
              // Auto-mark-read for messages that landed in an already-open
              // thread (ThreadList.onOpen only marks the snapshot at click
              // time). Gated on document.hasFocus(): when the window is not
              // in focus we leave the new message unread so the unread
              // counter surfaces it when the user returns.
              if (typeof document !== 'undefined' && document.hasFocus()) {
                const unread = msgs.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
                if (unread.length) {
                  client.markRead(unread)
                    .then(() => useStore.getState().markThreadRead(id))
                    .catch(err => console.warn('auto markRead failed', err))
                }
              }
            }
          }
          break
        }
        case 'AccountStatus': {
          const id = Number(ev.payload.account_id), state = String(ev.payload.state ?? 'ok')
          // Error events carry a human-readable `detail` (the dial/sync error
          // string from the account worker). Keep it in the store so the
          // sidebar and Settings can explain WHY an account is broken.
          const detail = ev.payload.detail != null ? String(ev.payload.detail) : undefined
          useStore.getState().setAccountDetail(id, state === 'error' ? (detail ?? 'Connection failed') : undefined)
          const list = await client.listAccounts()
          // ListAccounts returns a synthetic "ok" for every row (real status
          // only exists on this event bus), so for accounts OTHER than the
          // event's subject keep the live status already in the store —
          // otherwise one account reconnecting would repaint a still-broken
          // account as healthy.
          const prevById = new Map(useStore.getState().accounts.map(a => [a.id, a.status]))
          useStore.getState().setAccounts(list.map(a =>
            a.id === id ? { ...a, status: state } : { ...a, status: prevById.get(a.id) ?? a.status }))
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
              .then(fs => useStore.getState().setFolders(accId, fs))
              .catch(err => console.warn('listFolders refresh failed', err))
          }
          // Scope gate: skip the threads/openThread refetch when we know
          // for sure the user's view doesn't intersect the affected folder.
          // Unlike MessageInserted (which can land in any folder the user
          // is currently viewing via Unread/Flagged virtual rows), the
          // payload here gives us the EXACT folder. If the user has a
          // narrowed filter that pins to a different account or folder,
          // there's nothing to refresh in their current threads list.
          const s = useStore.getState()
          const reqFilter = s.filter
          const accountMismatches = reqFilter.accountId !== undefined && reqFilter.accountId !== accId
          const folderMismatches = reqFilter.folderId !== undefined && reqFilter.folderId !== folderId
          if (accountMismatches || folderMismatches) {
            // Folder counts already refreshed above. Open thread can't be in
            // the affected folder if the active filter excludes it (the
            // thread row wouldn't have been visible to open in the first
            // place — except if the filter changed since opening, which is
            // a corner case where a stale "read" indicator self-heals on
            // the next interaction).
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
          const now = useStore.getState()
          if (filterSig(now.filter, now.activeProfileId) === reqSig) {
            now.setThreads(result)
          }
          const after = useStore.getState()
          if (after.openThreadId !== undefined) {
            const id = after.openThreadId
            const msgs = await client.getThread(id)
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
          // Re-read openThreadId; a snapshot at handler entry would re-open
          // a thread the user dismissed during the prior await. Then re-check
          // once more after getThread resolves so a thread close DURING the
          // fetch is honored (same race as the MessageInserted branch).
          const fresh = useStore.getState()
          if (fresh.openThreadId === undefined) break
          // Gate the refetch on whether the ready message is part of the
          // currently-open thread. AttachmentReady fires for every download
          // across every account; without this gate, opening a single thread
          // re-fetches it on every background download anywhere in the app.
          // When payload.message_id is missing (older event format) we fall
          // back to refetching — better a redundant fetch than a stale view.
          const msgId = ev.payload && (ev.payload as { message_id?: unknown }).message_id
          const msgIdNum = typeof msgId === 'number' ? msgId : Number(msgId)
          if (Number.isFinite(msgIdNum) && fresh.openThread) {
            const inThread = fresh.openThread.some(m => m.id === msgIdNum)
            if (!inThread) break
          }
          const id = fresh.openThreadId
          const msgs = await client.getThread(id)
          const final = useStore.getState()
          if (final.openThreadId === id) {
            final.setOpenThread(id, msgs)
          }
          break
        }
      }
    })
  }, [])

  // While the window was unfocused, the SSE handler above kept openThread
  // in sync but skipped mark-read for any newly arrived messages (intentional:
  // we don't auto-clear the unread badge for emails the user hasn't seen).
  // When focus returns, the user IS now looking at the open thread, so flush
  // any unread it accumulated.
  useEffect(() => {
    const onFocus = () => {
      const s = useStore.getState()
      const id = s.openThreadId
      if (id === undefined || !s.openThread) return
      const unread = s.openThread.filter(m => !(m.flags ?? []).includes('\\Seen')).map(m => m.id)
      if (unread.length === 0) return
      client.markRead(unread)
        .then(() => useStore.getState().markThreadRead(id))
        .catch(err => console.warn('focus markRead failed', err))
    }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [])
}
