import type { MessageDTO } from '../api/types'

// StoreFilter mirrors the store-side filter shape (camelCase) used by the
// frontend. The wire-side ThreadFilter is snake_case and lives in api/types.
export type StoreFilter = {
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
export function filterSig(f: StoreFilter, profileId: number | null): string {
  return `${profileId ?? ''}|${f.accountId ?? ''}|${f.folderId ?? ''}|${f.unreadOnly ? 1 : 0}|${f.hasFlagged ? 1 : 0}`
}

// scopeThreadMessages filters thread messages to the active folder when a
// concrete folder (such as Inbox or Sent) is selected. All mail / Unread /
// Flagged views have folderId === undefined and display the entire thread.
export function scopeThreadMessages(msgs: MessageDTO[], folderId?: number): MessageDTO[] {
  if (folderId === undefined) return msgs
  return msgs.filter(m => m.folder_id === folderId)
}