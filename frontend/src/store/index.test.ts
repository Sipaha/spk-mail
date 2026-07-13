import { describe, it, expect, beforeEach } from 'vitest'
import { useStore } from './index'
import type { AccountDTO, FolderDTO, ProfileDTO } from '../api/types'

const baseFolders: Record<number, FolderDTO[]> = {
  1: [{ id: 10, account_id: 1, name: 'INBOX', role: 'inbox', unread_count: 1, total_count: 1, flagged_count: 0 }],
  2: [{ id: 20, account_id: 2, name: 'INBOX', role: 'inbox', unread_count: 0, total_count: 0, flagged_count: 0 }],
}

beforeEach(() => {
  useStore.setState({
    accounts: [],
    profiles: [],
    activeProfileId: null,
    threads: [],
    folders: {},
    filter: { unreadOnly: false, hasFlagged: false },
    search: '',
    syncProgress: {},
    writeError: null,
    bootstrapError: null,
  })
})

describe('useStore', () => {
  it('setAccounts prunes folder cache for removed accounts', () => {
    useStore.setState({ folders: { ...baseFolders } })
    const accounts: AccountDTO[] = [
      { id: 1, name: 'A', email: 'a@x', color: '#fff', status: 'ok' },
    ]
    useStore.getState().setAccounts(accounts)
    const { accounts: gotAccounts, folders } = useStore.getState()
    expect(gotAccounts).toEqual(accounts)
    expect(folders[1]).toBeDefined()
    expect(folders[2]).toBeUndefined()
  })

  it('setActiveProfile clears open thread state', () => {
    useStore.setState({
      activeProfileId: 1,
      openThreadId: 42,
      openThread: [{
        id: 1, account_id: 1, folder_id: 1,
        subject: 's', from_addr: 'a@b', to_addrs: [],
        date: 0, flags: [], body_html: '', body_text: '', attachments: [],
      }],
    })
    useStore.getState().setActiveProfile(2)
    const s = useStore.getState()
    expect(s.activeProfileId).toBe(2)
    expect(s.openThreadId).toBeUndefined()
    expect(s.openThread).toBeUndefined()
  })

  it('setProfiles resets activeProfileId when current profile is gone', () => {
    const profiles: ProfileDTO[] = [
      { id: 5, name: 'Work', color: '#000', sort_order: 0, muted: false },
      { id: 6, name: 'Personal', color: '#fff', sort_order: 1, muted: false },
    ]
    useStore.setState({ activeProfileId: 99 })
    useStore.getState().setProfiles(profiles)
    expect(useStore.getState().activeProfileId).toBe(5)
  })

  it('setProfiles keeps activeProfileId when still present', () => {
    const profiles: ProfileDTO[] = [
      { id: 5, name: 'Work', color: '#000', sort_order: 0, muted: false },
    ]
    useStore.setState({ activeProfileId: 5 })
    useStore.getState().setProfiles(profiles)
    expect(useStore.getState().activeProfileId).toBe(5)
  })
})