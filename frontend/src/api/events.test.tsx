import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import { useStore } from '../store'
import { useEventStream } from './events'
import { PAGE_SIZE } from '../lib/paging'
import type { ThreadDTO } from './types'

function makeThreads(n: number): ThreadDTO[] {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1, subject: `t${i}`, unread_count: 0, last_date: i, msg_count: 1,
    has_flagged: false, has_attach: false, account_id: 1, last_from: '', snippet: '',
  }))
}

const { listThreads, getThread, listFolders, listAccounts, markRead, eventHandlerRef } = vi.hoisted(() => {
  let eventHandler: ((ev: { type: string; payload: Record<string, unknown> }) => void | Promise<void>) | undefined
  return {
    listThreads: vi.fn(),
    getThread: vi.fn(),
    listFolders: vi.fn(),
    listAccounts: vi.fn(),
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
    listAccounts,
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
    threads: [{ id: 1, subject: 'seed', unread_count: 0, last_date: 0, msg_count: 1, has_flagged: false, has_attach: false, account_id: 1, last_from: '', snippet: '' }],
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
  listAccounts.mockResolvedValue([])
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

describe('useEventStream coalescing, AccountStatus, and folder-scoped markRead', () => {
  it('coalesces burst MessageInserted events without out-of-order overwrites', async () => {
    let resolveFirst!: (v: ThreadDTO[]) => void
    listThreads
      .mockReturnValueOnce(new Promise<ThreadDTO[]>((r) => { resolveFirst = r }))
      .mockResolvedValueOnce([{ ...makeThreads(1)[0], subject: 'latest-thread' }])

    render(<EventHarness />)
    const p1 = eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })
    const p2 = eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })
    const p3 = eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(1))

    resolveFirst([{ ...makeThreads(1)[0], subject: 'older-thread' }])
    await Promise.all([p1, p2, p3])

    expect(listThreads).toHaveBeenCalledTimes(2)
    expect(useStore.getState().threads[0]?.subject).toBe('latest-thread')
  })

  it('runs queued MessageInserted refresh when an in-flight listThreads rejects', async () => {
    let rejectFirst!: (err: Error) => void
    listThreads
      .mockReturnValueOnce(new Promise<ThreadDTO[]>((_, reject) => { rejectFirst = reject }))
      .mockResolvedValueOnce([{ ...makeThreads(1)[0], id: 99, subject: 'recovered-after-reject' }])

    render(<EventHarness />)
    const p1 = eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })
    await waitFor(() => expect(listThreads).toHaveBeenCalledTimes(1))

    // Second event arrives while first refresh is in flight; then first rejects.
    const p2 = eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })
    rejectFirst(new Error('transient refresh failure'))
    await Promise.all([p1, p2])

    expect(listThreads).toHaveBeenCalledTimes(2)
    expect(useStore.getState().threads[0]?.subject).toBe('recovered-after-reject')
  })

  it('updates AccountStatus synchronously and ignores stale listAccounts responses', async () => {
    let resolveErrList!: (v: Array<{ id: number; name: string; email: string; color: string; status: string; detail?: string }>) => void
    let resolveOkList!: (v: Array<{ id: number; name: string; email: string; color: string; status: string; detail?: string }>) => void
    listAccounts
      .mockReturnValueOnce(new Promise((r) => { resolveErrList = r }))
      .mockReturnValueOnce(new Promise((r) => { resolveOkList = r }))

    useStore.setState({
      accounts: [{ id: 1, name: 'Test', email: 'a@example.com', color: '#3b82f6', status: 'ok' }],
    })
    render(<EventHarness />)

    const pErr = eventHandlerRef.current!({
      type: 'AccountStatus',
      payload: { account_id: 1, state: 'error', detail: 'dial tcp: connection refused' },
    })
    expect(useStore.getState().accounts[0]?.status).toBe('error')
    expect(useStore.getState().accountDetail[1]).toBe('dial tcp: connection refused')

    const pOk = eventHandlerRef.current!({
      type: 'AccountStatus',
      payload: { account_id: 1, state: 'ok' },
    })
    expect(useStore.getState().accounts[0]?.status).toBe('ok')
    expect(useStore.getState().accountDetail[1]).toBeUndefined()

    // Newer #2 response resolves first with status 'ok', then stale #1 resolves with 'error'.
    resolveOkList([{ id: 1, name: 'Test', email: 'a@example.com', color: '#3b82f6', status: 'ok' }])
    await pOk
    resolveErrList([{ id: 1, name: 'Test', email: 'a@example.com', color: '#3b82f6', status: 'error', detail: 'dial tcp: connection refused' }])
    await pErr

    expect(listAccounts).toHaveBeenCalledTimes(2)
    expect(useStore.getState().accounts[0]?.status).toBe('ok')
    expect(useStore.getState().accountDetail[1]).toBeUndefined()
  })

  it('auto-mark-read only marks messages in the active folder', async () => {
    listThreads.mockResolvedValue([])
    getThread.mockResolvedValue([
      { id: 101, account_id: 1, folder_id: 10, uid: 1, date: 100, flags: [], attachments: [] },
      { id: 102, account_id: 1, folder_id: 20, uid: 1, date: 200, flags: [], attachments: [] },
    ])
    useStore.setState({
      openThreadId: 7,
      filter: { folderId: 10, unreadOnly: false, hasFlagged: false },
    })
    vi.spyOn(document, 'hasFocus').mockReturnValue(true)

    render(<EventHarness />)
    await eventHandlerRef.current!({ type: 'MessageInserted', payload: { account_id: 1 } })

    await waitFor(() => expect(markRead).toHaveBeenCalledWith([101]))
  })
})