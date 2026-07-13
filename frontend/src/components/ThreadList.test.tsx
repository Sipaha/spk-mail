import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import ThreadList from './ThreadList'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'

const { listThreads } = vi.hoisted(() => ({ listThreads: vi.fn() }))

vi.mock('../api/client', () => ({
  client: {
    listThreads,
    getThread: vi.fn(),
    markRead: vi.fn(),
    toggleThreadFlagged: vi.fn(),
  },
}))

function thread(id: number, subject: string): ThreadDTO {
  return {
    id, subject, last_date: id, msg_count: 1, unread_count: 0,
    has_flagged: false, has_attach: false, last_from: 'a@b.com', snippet: '',
  }
}

// A deferred promise per listThreads() call, so the test can resolve calls
// out of order to simulate a slow response from an earlier filter scope
// landing after a later one already resolved.
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}

beforeEach(() => {
  vi.clearAllMocks()
  useStore.setState({
    accounts: [], profiles: [], threads: [], folders: {},
    activeProfileId: null,
    filter: { unreadOnly: false, hasFlagged: false },
    openThreadId: undefined, openThread: undefined,
    syncProgress: {}, writeError: null, bootstrapError: null,
  })
})

describe('ThreadList filter-switch generation guard', () => {
  it('applies only the latest filter scope\'s response when an earlier one resolves later', async () => {
    const d1 = deferred<ThreadDTO[]>()
    const d2 = deferred<ThreadDTO[]>()
    const d3 = deferred<ThreadDTO[]>()
    listThreads.mockReturnValueOnce(d1.promise)
    listThreads.mockReturnValueOnce(d2.promise)
    listThreads.mockReturnValueOnce(d3.promise)

    render(<ThreadList />)
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(1))

    // Switch filter scope twice in a row before the first request resolves.
    act(() => { useStore.getState().setFilter({ folderId: 2 }) })
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(2))
    act(() => { useStore.getState().setFilter({ folderId: 3 }) })
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(3))

    // Resolve out of order: the newest request (scope C) first, then the
    // two stale ones. Only C's data must ever reach the screen.
    act(() => { d3.resolve([thread(30, 'scope-C')]) })
    await screen.findByText('scope-C')

    act(() => { d1.resolve([thread(10, 'scope-A')]) })
    act(() => { d2.resolve([thread(20, 'scope-B')]) })

    // Give any (incorrect) state update a chance to land, then assert the
    // stale scopes never overwrote the current list.
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByText('scope-A')).toBeNull()
    expect(screen.queryByText('scope-B')).toBeNull()
    expect(screen.getByText('scope-C')).toBeTruthy()
    expect(useStore.getState().threads.map(t => t.subject)).toEqual(['scope-C'])
  })

  it('does not append a stale load-more page after the filter scope changes', async () => {
    const PAGE_SIZE = 200
    const page1 = Array.from({ length: PAGE_SIZE }, (_, i) => thread(i + 1, `p1-${i}`))
    listThreads.mockResolvedValueOnce(page1)

    render(<ThreadList />)
    await screen.findByText('p1-0')
    expect(useStore.getState().threads).toHaveLength(PAGE_SIZE)

    const dMore = deferred<ThreadDTO[]>()
    listThreads.mockReturnValueOnce(dMore.promise)
    fireEvent.click(screen.getByRole('button', { name: /load more/i }))
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(2))

    // Switch filter scope while the load-more request is still in flight.
    const dNewScope = deferred<ThreadDTO[]>()
    listThreads.mockReturnValueOnce(dNewScope.promise)
    act(() => { useStore.getState().setFilter({ folderId: 99 }) })
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(3))

    act(() => { dNewScope.resolve([thread(500, 'new-scope-thread')]) })
    await screen.findByText('new-scope-thread')

    // Now let the stale load-more page resolve. Its rows must never be
    // appended to the new scope's (already-applied) list.
    const stalePage = Array.from({ length: PAGE_SIZE }, (_, i) => thread(1000 + i, `stale-p2-${i}`))
    act(() => { dMore.resolve(stalePage) })
    await new Promise((r) => setTimeout(r, 0))

    expect(screen.queryByText('stale-p2-0')).toBeNull()
    expect(useStore.getState().threads.map(t => t.subject)).toEqual(['new-scope-thread'])
  })
})
