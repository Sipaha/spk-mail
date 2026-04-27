import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import FolderTree from './FolderTree'
import { useStore } from '../store'

vi.mock('../api/client', () => ({
  client: {
    listFolders: () => Promise.resolve([
      { id: 10, account_id: 1, name: 'INBOX', role: 'inbox',  unread_count: 3 },
      { id: 11, account_id: 1, name: 'Sent',  role: 'sent',   unread_count: 0 },
    ]),
  },
}))

beforeEach(() => useStore.setState({
  accounts: [], profiles: [], threads: [], folders: {},
  activeProfileId: null,
  filter: { unreadOnly: false, hasFlagged: false },
  syncProgress: {},
}))

describe('FolderTree', () => {
  it('renders folders with unread badges after load', async () => {
    render(<FolderTree accountId={1} />)
    expect(await screen.findByText('INBOX')).toBeTruthy()
    expect(await screen.findByText('Sent')).toBeTruthy()
    expect(screen.getByText('3')).toBeTruthy()
  })

  it('clicking a folder updates filter.folderId', async () => {
    render(<FolderTree accountId={1} />)
    fireEvent.click(await screen.findByText('Sent'))
    await waitFor(() => expect(useStore.getState().filter.folderId).toBe(11))
    expect(useStore.getState().filter.accountId).toBe(1)
  })
})
