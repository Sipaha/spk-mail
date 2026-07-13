import { describe, it, expect, vi, beforeEach } from 'vitest'
import { StrictMode } from 'react'
import { render, screen, waitFor } from '@testing-library/react'
import App from './App'
import { useStore } from './store'

const { listAccounts, listProfiles, getThread, markRead } = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listProfiles: vi.fn(),
  getThread: vi.fn(),
  markRead: vi.fn(),
}))

vi.mock('./api/client', () => ({
  client: {
    listAccounts,
    listProfiles,
    subscribeEvents: () => () => {},
    getThread,
    markRead,
    listThreads: vi.fn().mockResolvedValue([]),
    listFolders: vi.fn().mockResolvedValue([]),
  },
}))

beforeEach(() => {
  vi.clearAllMocks()
  listAccounts.mockResolvedValue([{ id: 1, name: 'Test Personal', email: 'a@b.com', color: '#3b82f6', status: 'ok' }])
  listProfiles.mockResolvedValue([])
  getThread.mockResolvedValue([])
  markRead.mockResolvedValue(undefined)
  useStore.setState({
    accounts: [], profiles: [], activeProfileId: null, threads: [], folders: {},
    filter: { unreadOnly: false, hasFlagged: false }, search: '',
    openThreadId: undefined, openThread: undefined, syncProgress: {},
    writeError: null, bootstrapError: null,
  })
})

describe('App bootstrap runs on every route', () => {
  it('fetches accounts/profiles on a cold #/settings load', async () => {
    window.location.hash = '#/settings/accounts'
    render(<App />)
    await screen.findByText('Test Personal')
    expect(listAccounts).toHaveBeenCalledTimes(1)
    expect(listProfiles).toHaveBeenCalledTimes(1)
  })

  it('fetches accounts/profiles on a cold #/add-account load', async () => {
    window.location.hash = '#/add-account'
    render(<App />)
    await screen.findByLabelText('Display name')
    expect(listAccounts).toHaveBeenCalledTimes(1)
    expect(listProfiles).toHaveBeenCalledTimes(1)
  })

  it('surfaces a retryable bootstrap error banner on the settings route', async () => {
    listAccounts.mockRejectedValueOnce(new Error('network down'))
    window.location.hash = '#/settings/accounts'
    render(<App />)
    await screen.findByText(/Failed to load accounts and profiles: network down/)

    listAccounts.mockResolvedValueOnce([{ id: 1, name: 'Test Personal', email: 'a@b.com', color: '#3b82f6', status: 'ok' }])
    screen.getByRole('button', { name: /retry/i }).click()
    await screen.findByText('Test Personal')
  })
})

describe('tray deep-link', () => {
  it('opens the thread once, even under StrictMode double-invoke', async () => {
    // The tray click lands the app on #/thread/<id>. StrictMode runs the effect
    // twice against the same render, so without the id guard this would fire a
    // second getThread (and a second markRead) for one notification.
    getThread.mockResolvedValue([
      { id: 10, account_id: 1, folder_id: 1, uid: 1, date: 0, flags: [], attachments: [] },
    ])
    window.location.hash = '#/thread/42'

    render(<StrictMode><App /></StrictMode>)

    await waitFor(() => expect(getThread).toHaveBeenCalledWith(42))
    await waitFor(() => expect(useStore.getState().openThread).toHaveLength(1))
    expect(getThread).toHaveBeenCalledTimes(1)
    expect(useStore.getState().openThreadId).toBe(42)
    // The hash is rewritten so a re-render doesn't re-trigger the deep link.
    expect(window.location.hash).toBe('#/')
  })

  it('marks the thread read when the deep-linked thread has unread messages', async () => {
    getThread.mockResolvedValue([
      { id: 10, account_id: 1, folder_id: 1, uid: 1, date: 0, flags: [], attachments: [] },
    ])
    window.location.hash = '#/thread/7'

    render(<StrictMode><App /></StrictMode>)

    await waitFor(() => expect(markRead).toHaveBeenCalledWith([10]))
    expect(markRead).toHaveBeenCalledTimes(1)
  })
})
