import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import { Profiler, StrictMode } from 'react'
import ThreadList from './ThreadList'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'

const { listThreads, getThread, markRead } = vi.hoisted(() => ({ listThreads: vi.fn(), getThread: vi.fn(), markRead: vi.fn() }))

vi.mock('../api/client', () => ({
  client: {
    listThreads,
    getThread,
    markRead,
    toggleThreadFlagged: vi.fn(),
  },
}))

function thread(id: number, subject: string): ThreadDTO {
  return {
    id, subject, last_date: id, msg_count: 1, unread_count: 0,
    has_flagged: false, has_attach: false, account_id: 1, last_from: 'a@b.com', snippet: '',
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

describe('ThreadList pinned rows settle after server filtering', () => {
  it.each(['unread', 'flagged'] as const)('keeps a filtered-out %s thread without continuously committing', async (scope) => {
    const opened = { ...thread(2, 'Pinned notification'), unread_count: 1, has_flagged: true }
    const other = thread(1, 'Other notification')
    listThreads.mockResolvedValue([opened, other])
    getThread.mockResolvedValue([{ id: 20, flags: [] }])
    markRead.mockResolvedValue(undefined)
    useStore.getState().setFilter(scope === 'unread' ? { unreadOnly: true } : { hasFlagged: true })

    let commits = 0
    render(<StrictMode><Profiler id="thread-list" onRender={() => {
      // Fail a feedback loop deterministically instead of letting act() hang.
      if (++commits > 40) throw new Error('Pinned thread list did not settle')
    }}><ThreadList /></Profiler></StrictMode>)
    await screen.findByText('Pinned notification')
    fireEvent.click(screen.getByText('Pinned notification'))
    await waitFor(() => expect(markRead).toHaveBeenCalledWith([20]))

    // An event refetch now excludes the read/unflagged thread. It must stay
    // visible while open, even when it is the only remaining pinned row.
    act(() => { useStore.getState().setThreads([other]) })
    expect(screen.getByText('Pinned notification')).toBeTruthy()
    act(() => { useStore.getState().setThreads([]) })
    expect(screen.getByText('Pinned notification')).toBeTruthy()
    const settled = commits
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(commits).toBe(settled)

    // A later server refresh must replace the snapshot without duplicating it.
    act(() => { useStore.getState().setThreads([{ ...opened, subject: 'Updated notification', unread_count: 0 }]) })
    expect(screen.queryByText('Pinned notification')).toBeNull()
    expect(screen.getAllByText('Updated notification')).toHaveLength(1)
    act(() => { useStore.getState().setThreads([]) })
    expect(screen.getByText('Updated notification')).toBeTruthy()

    // Closing also clears the pin and lets the existing leave animation finish.
    act(() => { useStore.getState().setOpenThread(undefined) })
    await act(async () => { await new Promise(r => setTimeout(r, 300)) })
    expect(screen.queryByText('Updated notification')).toBeNull()
    act(() => {
      useStore.getState().setThreads([{ ...opened, subject: 'Updated notification' }])
      useStore.getState().setOpenThread(opened.id)
    })
    act(() => { useStore.getState().setThreads([]) })
    expect(screen.getByText('Updated notification')).toBeTruthy()

    // Changing the filter clears the retained row rather than leaking it into
    // another scope. Keep the next request pending to observe that boundary.
    listThreads.mockReturnValue(new Promise(() => {}))
    act(() => { useStore.getState().setFilter({ folderId: 99 }) })
    expect(screen.queryByText('Updated notification')).toBeNull()
  })
})
