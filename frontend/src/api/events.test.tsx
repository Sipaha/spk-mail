import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { useStore } from '../store'
import { useEventStream } from './events'
import { PAGE_SIZE } from '../lib/paging'
import type { ThreadDTO } from './types'

function makeThreads(n: number): ThreadDTO[] {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1, subject: `t${i}`, unread_count: 0, last_date: i, msg_count: 1,
    has_flagged: false, has_attach: false, last_from: '', snippet: '',
  }))
}

const { listThreads, getThread, listFolders, markRead, eventHandlerRef } = vi.hoisted(() => {
  let eventHandler: ((ev: { type: string; payload: Record<string, unknown> }) => void | Promise<void>) | undefined
  return {
    listThreads: vi.fn(),
    getThread: vi.fn(),
    listFolders: vi.fn(),
    markRead: vi.fn(),
    eventHandlerRef: {
      get current() { return eventHandler },
      set current(cb) { eventHandler = cb },
    },
  }
})

vi.mock('./client', () => ({
  client: {
    subscribeEvents: (cb: typeof eventHandlerRef.current) => {
      eventHandlerRef.current = cb
      return () => {}
    },
    listThreads,
    getThread,
    listFolders,
    markRead,
  },
}))

function EventHarness() {
  useEventStream()
  return null
}

beforeEach(() => {
  vi.clearAllMocks()
  eventHandlerRef.current = undefined
  useStore.setState({
    accounts: [],
    profiles: [],
    activeProfileId: null,
    threads: [{ id: 1, subject: 'seed', unread_count: 0, last_date: 0, msg_count: 1, has_flagged: false, has_attach: false, last_from: '', snippet: '' }],
    folders: {},
    filter: { unreadOnly: false, hasFlagged: false },
    search: '',
    openThreadId: undefined,
    openThread: undefined,
    syncProgress: {},
    writeError: null,
    bootstrapError: null,
  })
  listFolders.mockResolvedValue([])
  markRead.mockResolvedValue(undefined)
})

describe('useEventStream stale-fetch guards', () => {
  it('drops listThreads result when filter changes during fetch', async () => {
    let resolveThreads!: (v: unknown[]) => void
    listThreads.mockReturnValue(new Promise((resolve) => { resolveThreads = resolve }))

    render(<EventHarness />)
    expect(eventHandlerRef.current).toBeDefined()

    const handlerDone = eventHandlerRef.current!({
      type: 'MessageInserted',
      payload: { account_id: 1 },
    })
    await waitFor(() => expect(listThreads).toHaveBeenCalled())

    useStore.getState().setFilter({ folderId: 99 })
    resolveThreads([{ id: 2, subject: 'stale', unread_count: 1, last_date: 1, account_ids: [1], has_flagged: false }])
    await handlerDone

    const threads = useStore.getState().threads
    expect(threads).toHaveLength(1)
    expect(threads[0]?.subject).toBe('seed')
  })

  it('does not reopen thread when openThreadId cleared during getThread', async () => {
    listThreads.mockResolvedValue([])
    let resolveThread!: (v: unknown[]) => void
    getThread.mockReturnValue(new Promise((resolve) => { resolveThread = resolve }))

    useStore.setState({ openThreadId: 7 })
    render(<EventHarness />)

    const handlerDone = eventHandlerRef.current!({
      type: 'MessageInserted',
      payload: { account_id: 1 },
    })
    await waitFor(() => expect(getThread).toHaveBeenCalledWith(7))

    useStore.getState().setOpenThread(undefined)
    resolveThread([{ id: 1, account_id: 1, folder_id: 1, uid: 1, date: 0, flags: [], attachments: [] }])
    await handlerDone

    const s = useStore.getState()
    expect(s.openThreadId).toBeUndefined()
    expect(s.openThread).toBeUndefined()
  })
})

// Both refetch call sites (the MessageInserted/MessageUpdated/MessageArrived
// branch and the FolderMarkedRead branch) used to ask for a hardcoded
// limit: 200, which silently collapsed any pages the user had loaded via
// "Load more". They now call refetchLimit(s.threads.length) so a wholesale
// refresh preserves what's already loaded. These tests pin the requested
// limit at both call sites; reverting either one back to a bare `limit: 200`
// must fail the corresponding "many threads loaded" case below.
describe('useEventStream refetch limit preserves loaded pages', () => {
  it('MessageInserted requests limit=PAGE_SIZE when fewer threads than a page are loaded', async () => {
    listThreads.mockResolvedValue([])
    render(<EventHarness />)

    await eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })

    expect(listThreads).toHaveBeenCalledWith(expect.objectContaining({ limit: PAGE_SIZE }))
  })

  it('MessageInserted requests limit=loaded count when more than a page is loaded', async () => {
    listThreads.mockResolvedValue([])
    useStore.setState({ threads: makeThreads(400) })
    render(<EventHarness />)

    await eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })

    expect(listThreads).toHaveBeenCalledWith(expect.objectContaining({ limit: 400 }))
  })

  it('MessageUpdated requests limit=loaded count when more than a page is loaded', async () => {
    listThreads.mockResolvedValue([])
    useStore.setState({ threads: makeThreads(400) })
    render(<EventHarness />)

    await eventHandlerRef.current!({ type: 'MessageUpdated', payload: { account_id: 1 } })

    expect(listThreads).toHaveBeenCalledWith(expect.objectContaining({ limit: 400 }))
  })

  it('FolderMarkedRead requests limit=loaded count when more than a page is loaded', async () => {
    listThreads.mockResolvedValue([])
    useStore.setState({ threads: makeThreads(400) })
    render(<EventHarness />)

    await eventHandlerRef.current!({
      type: 'FolderMarkedRead',
      payload: { account_id: 1, folder_id: 1 },
    })

    expect(listThreads).toHaveBeenCalledWith(expect.objectContaining({ limit: 400 }))
  })
})