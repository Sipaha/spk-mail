import { useEffect } from 'react'
import { client } from './client'
import { useStore } from '../store'

// StoreFilter mirrors the store-side filter shape (camelCase) used by the
// frontend. The wire-side ThreadFilter is snake_case and lives in ./types —
// we only need the camelCase form here for the signature compare.
type StoreFilter = {
  accountId?: number
  folderId?: number
  unreadOnly: boolean
  hasFlagged: boolean
}

// filterSig serialises the (filter, profile) pair so two snapshots can be
// compared by VALUE rather than by reference. `setFilter` always produces a
// new object reference even when the user clicks the already-active view, so
// JS `===` would falsely report a "filter change" between snapshots taken
// before and after a no-op store update.
function filterSig(f: StoreFilter, profileId: number | null): string {
  return `${profileId ?? ''}|${f.accountId ?? ''}|${f.folderId ?? ''}|${f.unreadOnly ? 1 : 0}|${f.hasFlagged ? 1 : 0}`
}

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
            limit: 200,
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
            }
          }
          break
        }
        case 'AccountStatus': {
          const id = Number(ev.payload.account_id), state = String(ev.payload.state ?? 'ok')
          const list = await client.listAccounts()
          useStore.getState().setAccounts(list.map(a => a.id === id ? { ...a, status: state } : a))
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
        case 'WriteError':
          // surface in console for now; toast UI is plan 7
          console.error('write error', ev.payload); break
        case 'AttachmentReady': {
          // Re-read openThreadId; a snapshot at handler entry would re-open
          // a thread the user dismissed during the prior await. Then re-check
          // once more after getThread resolves so a thread close DURING the
          // fetch is honored (same race as the MessageInserted branch).
          const fresh = useStore.getState()
          if (fresh.openThreadId !== undefined) {
            const id = fresh.openThreadId
            const msgs = await client.getThread(id)
            const final = useStore.getState()
            if (final.openThreadId === id) {
              final.setOpenThread(id, msgs)
            }
          }
          break
        }
      }
    })
  }, [])
}
