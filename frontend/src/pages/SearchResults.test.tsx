import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import SearchResults from './SearchResults'
import { useStore } from '../store'
import type { MessageDTO, SearchHitDTO, ThreadDTO } from '../api/types'

const { search, getThread, markRead } = vi.hoisted(() => ({
  search: vi.fn(),
  getThread: vi.fn(),
  markRead: vi.fn(),
}))

vi.mock('../api/client', () => ({
  client: { search, getThread, markRead },
}))

function hit(overrides: Partial<SearchHitDTO> = {}): SearchHitDTO {
  return {
    message_id: 1, thread_id: 100, subject: 'Hello', from_addr: 'a@b',
    date: 1, snippet: 'hi', ...overrides,
  }
}

function msg(overrides: Partial<MessageDTO> = {}): MessageDTO {
  return {
    id: 1, account_id: 1, folder_id: 1, subject: 'Hello', from_addr: 'a@b',
    to_addrs: [], date: 1, flags: [], body_html: '', body_text: '', attachments: [],
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  window.location.hash = ''
  useStore.setState({
    accounts: [], profiles: [], threads: [], folders: {},
    activeProfileId: null,
    filter: { unreadOnly: false, hasFlagged: false },
    openThreadId: undefined, openThread: undefined,
    syncProgress: {}, writeError: null, bootstrapError: null,
  })
})

function threadDTO(overrides: Partial<ThreadDTO> = {}): ThreadDTO {
  return {
    id: 100, subject: 'Hello', last_date: 1, msg_count: 1, unread_count: 1,
    has_flagged: false, has_attach: false, account_id: 1, last_from: 'a@b', snippet: '',
    ...overrides,
  }
}

describe('SearchResults opening a thread', () => {
  it('marks unread messages read, the same way the main list does', async () => {
    // Seed the store's threads list with the same thread the search hit
    // points at, so markThreadRead's effect (unread_count -> 0) is observable
    // the same way ThreadList/App.tsx's callers verify it.
    useStore.setState({ threads: [threadDTO({ unread_count: 3 })] })
    search.mockResolvedValueOnce([hit()])
    const unreadMsg = msg({ id: 5, flags: [] })
    getThread.mockResolvedValueOnce([unreadMsg])
    markRead.mockResolvedValueOnce(undefined)

    render(<SearchResults query="hello" />)
    await screen.findByText('Hello')

    fireEvent.click(screen.getByText('Hello'))

    await waitFor(() => expect(getThread).toHaveBeenCalledWith(100))
    await waitFor(() => expect(markRead).toHaveBeenCalledWith([5]))
    await waitFor(() => expect(useStore.getState().openThreadId).toBe(100))
    await waitFor(() => expect(useStore.getState().threads.find(t => t.id === 100)?.unread_count).toBe(0))
  })

  it('does not call markRead when the thread has no unread messages', async () => {
    search.mockResolvedValueOnce([hit()])
    getThread.mockResolvedValueOnce([msg({ id: 5, flags: ['\\Seen'] })])

    render(<SearchResults query="hello" />)
    await screen.findByText('Hello')

    fireEvent.click(screen.getByText('Hello'))

    await waitFor(() => expect(getThread).toHaveBeenCalledWith(100))
    await new Promise(r => setTimeout(r, 0))
    expect(markRead).not.toHaveBeenCalled()
  })
})
