import { describe, it, expect, vi, beforeEach } from 'vitest'

beforeEach(() => { vi.resetModules() })

describe('client', () => {
  it('uses HTTP when window.wails is missing', async () => {
    delete (window as any).wails
    const fetchMock = vi.fn(async () => new Response(JSON.stringify([]), { headers: { 'content-type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    const { client } = await import('./client')
    await client.listAccounts()
    expect(fetchMock).toHaveBeenCalledWith('/api/ListAccounts', expect.objectContaining({ method: 'POST' }))
  })
})
