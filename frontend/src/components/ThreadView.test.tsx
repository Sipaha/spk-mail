import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import ThreadView from './ThreadView'
import AccountSidebar from './AccountSidebar'
import { useStore } from '../store'
import type { MessageDTO } from '../api/types'

function makeMsg(id: number, folderId: number, from: string, body: string): MessageDTO {
  return {
    id,
    account_id: 1,
    folder_id: folderId,
    subject: 'Ticket 42',
    from_addr: from,
    to_addrs: ['alice@example.com'],
    date: id * 100,
    flags: ['\\Seen'],
    body_html: '',
    body_text: body,
    attachments: [],
  }
}

beforeEach(() => {
  useStore.setState({
    accounts: [{ id: 1, name: 'Personal', email: 'alice@example.com', color: '#3b82f6', status: 'ok' }],
    profiles: [],
    activeProfileId: null,
    threads: [],
    folders: {},
    filter: { unreadOnly: false, hasFlagged: false },
    search: '',
    openThreadId: 1,
    openThread: [
      makeMsg(1, 10, 'Support <support@example.test>', 'Incoming body'),
      makeMsg(2, 20, 'Alice <alice@example.com>', 'Sent reply body'),
    ],
    syncProgress: {},
    accountDetail: {},
    writeError: null,
    bootstrapError: null,
  })
})

describe('ThreadView folder scoping', () => {
  it('shows all folder messages in All mail (folderId undefined)', () => {
    render(<ThreadView />)
    expect(screen.getByText('2 messages')).toBeTruthy()
    expect(screen.getByText('Support <support@example.test>')).toBeTruthy()
    expect(screen.getByText('Alice <alice@example.com>')).toBeTruthy()
  })

  it('hides Sent messages when Inbox folderId is active', () => {
    useStore.setState({ filter: { accountId: 1, folderId: 10, unreadOnly: false, hasFlagged: false } })
    render(<ThreadView />)
    expect(screen.getByText('1 message')).toBeTruthy()
    expect(screen.getByText('Support <support@example.test>')).toBeTruthy()
    expect(screen.queryByText('Alice <alice@example.com>')).toBeNull()
  })

  it('renders empty-in-folder placeholder when active folder has no messages from open thread', () => {
    useStore.setState({ filter: { accountId: 1, folderId: 99, unreadOnly: false, hasFlagged: false } })
    render(<ThreadView />)
    expect(screen.getByText('No messages in this folder')).toBeTruthy()
  })
})

describe('AccountSidebar status display', () => {
  it('shows Can\'t connect when account status is error instead of Synced', () => {
    useStore.setState({
      accounts: [{ id: 1, name: 'Personal', email: 'alice@example.com', color: '#3b82f6', status: 'error', detail: 'EOF' }],
      accountDetail: { 1: 'EOF' },
    })
    render(<AccountSidebar />)
    expect(screen.getByText("Can't connect — details in Settings")).toBeTruthy()
    expect(screen.queryByText('Synced')).toBeNull()
  })

  it('shows Synced only when status is ok and not syncing', () => {
    render(<AccountSidebar />)
    expect(screen.getByText('Synced')).toBeTruthy()
  })
})
