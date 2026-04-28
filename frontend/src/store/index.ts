import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { AccountDTO, FolderDTO, MessageDTO, ProfileDTO, ThreadDTO } from '../api/types'

interface State {
  accounts: AccountDTO[]
  profiles: ProfileDTO[]
  activeProfileId: number | null
  threads: ThreadDTO[]
  folders: Record<number, FolderDTO[]>
  filter: { accountId?: number; folderId?: number; unreadOnly: boolean; hasFlagged: boolean }
  openThreadId?: number
  openThread?: MessageDTO[]
  syncProgress: Record<number, { folder: string; folderId?: number; done: number; total: number }>

  setAccounts: (a: AccountDTO[]) => void
  upsertAccount: (a: AccountDTO) => void
  setProfiles: (p: ProfileDTO[]) => void
  setActiveProfile: (id: number | null) => void
  setThreads: (t: ThreadDTO[]) => void
  setFolders: (accountId: number, fs: FolderDTO[]) => void
  markThreadRead: (id: number) => void
  setOpenThread: (id: number | undefined, msgs?: MessageDTO[]) => void
  setFilter: (f: Partial<State['filter']>) => void
  setSyncProgress: (accId: number, folder: string, done: number, total: number, folderId?: number) => void
}

export const useStore = create<State>()(
  persist(
    (set) => ({
      accounts: [],
      profiles: [],
      activeProfileId: null,
      threads: [],
      folders: {},
      filter: { unreadOnly: false, hasFlagged: false },
      syncProgress: {},

      setAccounts: (a) => set({ accounts: a }),
      upsertAccount: (a) => set((s) => ({ accounts: [...s.accounts.filter(x => x.id !== a.id), a].sort((x,y)=>x.id-y.id) })),
      setProfiles: (p) => set((s) => {
        let next = s.activeProfileId
        if (next === null || !p.some(x => x.id === next)) {
          next = p[0]?.id ?? null
        }
        return { profiles: p, activeProfileId: next }
      }),
      // Switching profile must clear any thread the user had open in the
      // previous profile — otherwise events.ts keeps re-fetching it on every
      // MessageInserted and the previous profile's thread leaks across the
      // boundary into ThreadView under the new (potentially empty) profile.
      setActiveProfile: (id) => set({ activeProfileId: id, openThreadId: undefined, openThread: undefined }),
      setThreads: (t) => set({ threads: t }),
      setFolders: (accountId, fs) => set((s) => ({ folders: { ...s.folders, [accountId]: fs } })),
      markThreadRead: (id) => set((s) => ({ threads: s.threads.map(t => t.id === id ? { ...t, unread_count: 0 } : t) })),
      setOpenThread: (id, msgs) => set({ openThreadId: id, openThread: msgs }),
      setFilter: (f) => set((s) => ({ filter: { ...s.filter, ...f } })),
      setSyncProgress: (accId, folder, done, total, folderId) => set((s) => ({ syncProgress: { ...s.syncProgress, [accId]: { folder, folderId, done, total } } })),
    }),
    {
      name: 'spk-mail.activeProfile',
      partialize: (s) => ({ activeProfileId: s.activeProfileId }),
      onRehydrateStorage: () => (_state, error) => {
        if (error) {
          console.warn('persist rehydrate failed (localStorage disabled?)', error)
        }
      },
    },
  ),
)
