import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import ThreadRow from './ThreadRow'
import { useStore } from '../store'
import type { ThreadDTO } from '../api/types'

const { toggleThreadFlagged } = vi.hoisted(() => ({ toggleThreadFlagged: vi.fn() }))

vi.mock('../api/client', () => ({
  client: {
    toggleThreadFlagged,
  },
}))

function thread(overrides: Partial<ThreadDTO> = {}): ThreadDTO {
  return {
    id: 1, subject: 'Hello', last_date: 1, msg_count: 1, unread_count: 0,
    has_flagged: false, has_attach: false, account_id: 1, last_from: 'Bob <b@x>', snippet: '',
    ...overrides,
  }
}

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
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

describe('ThreadRow flag toggle', () => {
  it('flips has_flagged in the store immediately, before the round-trip resolves', async () => {
    const t = thread({ has_flagged: false })
    useStore.setState({ threads: [t] })
    const d = deferred<{ action: string; count: number }>()
    toggleThreadFlagged.mockReturnValueOnce(d.promise)

    render(<ThreadRow t={t} onOpen={() => {}} />)

    fireEvent.click(screen.getByRole('button', { name: /flag thread/i }))

    // Optimistic: the store reflects the new state synchronously, without
    // waiting for toggleThreadFlagged's promise to settle.
    expect(useStore.getState().threads.find(x => x.id === 1)?.has_flagged).toBe(true)
    expect(toggleThreadFlagged).toHaveBeenCalledWith(1)

    await act(async () => { d.resolve({ action: 'added', count: 1 }) })
    expect(useStore.getState().threads.find(x => x.id === 1)?.has_flagged).toBe(true)
  })

  it('rolls back the optimistic flip when the server call fails', async () => {
    const t = thread({ has_flagged: false })
    useStore.setState({ threads: [t] })
    const d = deferred<{ action: string; count: number }>()
    toggleThreadFlagged.mockReturnValueOnce(d.promise)

    render(<ThreadRow t={t} onOpen={() => {}} />)

    fireEvent.click(screen.getByRole('button', { name: /flag thread/i }))
    expect(useStore.getState().threads.find(x => x.id === 1)?.has_flagged).toBe(true)

    await act(async () => {
      d.reject(new Error('network down'))
      // let the rejection's .catch handler run
      await Promise.resolve().then(() => Promise.resolve())
    })

    expect(useStore.getState().threads.find(x => x.id === 1)?.has_flagged).toBe(false)
  })
})
