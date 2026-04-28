import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import FolderTree from './FolderTree'
import { useStore } from '../store'

vi.mock('../api/client', () => ({
  client: {
    listFolders: () => Promise.resolve([
      { id: 10, account_id: 1, name: 'INBOX', role: 'inbox', unread_count: 3, total_count: 250, flagged_count: 1 },
      { id: 11, account_id: 1, name: 'Sent',  role: 'sent',  unread_count: 0, total_count: 26,  flagged_count: 0 },
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
  it('renders virtual Unread/Flagged rows plus folders with counts after load', async () => {
    render(<FolderTree accountId={1} />)
    expect(await screen.findByText('Unread')).toBeTruthy()
    expect(screen.getByText('Flagged')).toBeTruthy()
    expect(await screen.findByText('INBOX')).toBeTruthy()
    expect(screen.getByText('Sent')).toBeTruthy()
    // unread badge for INBOX (3) and the virtual Unread row aggregate (also 3) — two matches.
    expect(screen.getAllByText('3')).toHaveLength(2)
    // total counts (rendered as "/ 250" and "/ 26")
    expect(screen.getByText(/\/ 250/)).toBeTruthy()
    expect(screen.getByText(/\/ 26/)).toBeTruthy()
    // virtual Flagged row aggregate badge = 1.
    expect(screen.getByText('1')).toBeTruthy()
  })

  it('clicking a folder updates filter.folderId', async () => {
    render(<FolderTree accountId={1} />)
    fireEvent.click(await screen.findByText('Sent'))
    await waitFor(() => expect(useStore.getState().filter.folderId).toBe(11))
    expect(useStore.getState().filter.accountId).toBe(1)
  })

  it('clicking the virtual Unread row sets unreadOnly=true and clears folderId', async () => {
    render(<FolderTree accountId={1} />)
    fireEvent.click(await screen.findByText('Unread'))
    await waitFor(() => expect(useStore.getState().filter.unreadOnly).toBe(true))
    expect(useStore.getState().filter.accountId).toBe(1)
    expect(useStore.getState().filter.folderId).toBeUndefined()
    expect(useStore.getState().filter.hasFlagged).toBe(false)
  })

  it('clicking the virtual Flagged row sets hasFlagged=true', async () => {
    render(<FolderTree accountId={1} />)
    fireEvent.click(await screen.findByText('Flagged'))
    await waitFor(() => expect(useStore.getState().filter.hasFlagged).toBe(true))
    expect(useStore.getState().filter.unreadOnly).toBe(false)
    expect(useStore.getState().filter.folderId).toBeUndefined()
  })
})
