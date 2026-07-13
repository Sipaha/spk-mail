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