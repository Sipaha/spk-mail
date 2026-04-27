import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { AccountDTO, MessageDTO, ProfileDTO, ThreadDTO } from '../api/types'

interface State {
  accounts: AccountDTO[]
  profiles: ProfileDTO[]
  activeProfileId: number | null
  threads: ThreadDTO[]
  filter: { accountId?: number; folderId?: number; unreadOnly: boolean }
  openThreadId?: number
  openThread?: MessageDTO[]
  syncProgress: Record<number, { folder: string; done: number; total: number }>

  setAccounts: (a: AccountDTO[]) => void
  upsertAccount: (a: AccountDTO) => void
  setProfiles: (p: ProfileDTO[]) => void
  setActiveProfile: (id: number | null) => void
  setThreads: (t: ThreadDTO[]) => void
  bumpThread: (id: number, lastDate: number) => void
  markThreadRead: (id: number) => void
  setOpenThread: (id: number | undefined, msgs?: MessageDTO[]) => void
  setFilter: (f: Partial<State['filter']>) => void
  setSyncProgress: (accId: number, folder: string, done: number, total: number) => void
}

export const useStore = create<State>()(
  persist(
    (set) => ({
      accounts: [],
      profiles: [],
      activeProfileId: null,
      threads: [],
      filter: { unreadOnly: false },
      syncProgress: {},

      setAccounts: (a) => set({ accounts: a }),
      upsertAccount: (a) => set((s) => ({ accounts: [...s.accounts.filter(x => x.id !== a.id), a].sort((x,y)=>x.id-y.id) })),
      setProfiles: (p) => set({ profiles: p }),
      setActiveProfile: (id) => set({ activeProfileId: id }),
      setThreads: (t) => set({ threads: t }),
      bumpThread: (id, lastDate) => set((s) => ({ threads: s.threads.map(t => t.id === id ? { ...t, last_date: lastDate } : t).sort((a,b)=>b.last_date-a.last_date) })),
      markThreadRead: (id) => set((s) => ({ threads: s.threads.map(t => t.id === id ? { ...t, unread_count: 0 } : t) })),
      setOpenThread: (id, msgs) => set({ openThreadId: id, openThread: msgs }),
      setFilter: (f) => set((s) => ({ filter: { ...s.filter, ...f } })),
      setSyncProgress: (accId, folder, done, total) => set((s) => ({ syncProgress: { ...s.syncProgress, [accId]: { folder, done, total } } })),
    }),
    {
      name: 'spk-mail.activeProfile',
      partialize: (s) => ({ activeProfileId: s.activeProfileId }),
    },
  ),
)
