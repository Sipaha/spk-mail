import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const callByName = vi.fn(async (_name: string, _arg?: unknown) => [] as unknown[])

vi.mock('@wailsio/runtime', () => ({
  Call: { ByName: (name: string, arg?: unknown) => callByName(name, arg) },
  Events: { On: vi.fn(() => () => {}) },
}))

beforeEach(() => {
  vi.resetModules()
  callByName.mockClear()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

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
    expect(callByName).not.toHaveBeenCalled()
  })

  it('uses Wails Call.ByName when location.protocol is wails:', async () => {
    vi.stubGlobal('location', { ...window.location, protocol: 'wails:' })
    const { client } = await import('./client')
    await client.listAccounts()
    expect(callByName).toHaveBeenCalledWith(
      'github.com/spk/spk-mail/internal/api/transport.API.ListAccounts',
      undefined,
    )
  })
})
