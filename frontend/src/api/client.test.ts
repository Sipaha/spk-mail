import { describe, it, expect, vi, beforeEach } from 'vitest'

beforeEach(() => { vi.resetModules() })

describe('client', () => {
  it('uses HTTP when location.protocol is not wails:', async () => {
    // jsdom defaults window.location.protocol to "http:" — that's the
    // browser-mode discriminator we expect to take the fetch path.
    expect(window.location.protocol).not.toBe('wails:')
    const fetchMock = vi.fn(async () => new Response(JSON.stringify([]), { headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const { client } = await import('./client')
    await client.listAccounts()
    expect(fetchMock).toHaveBeenCalledWith('/api/ListAccounts', expect.objectContaining({ method: 'POST' }))
  })
})
