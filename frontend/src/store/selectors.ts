import { useStore } from './index'

/** Total unread across all threads currently in the store. Used by the AccountSidebar badge. */
export const useTotalUnread = () =>
  useStore((s) => s.threads.reduce((n, t) => n + t.unread_count, 0))
