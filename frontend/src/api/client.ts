type Wails = { CallByName: (name: string, ...args: unknown[]) => Promise<unknown> }
declare global { interface Window { wails?: Wails } }

interface Client {
  listAccounts(): Promise<unknown[]>
}

const httpClient: Client = {
  async listAccounts() {
    const r = await fetch('/api/ListAccounts', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' })
    if (!r.ok) throw new Error(await r.text())
    return r.json()
  },
}

const wailsClient: Client = {
  async listAccounts() {
    return (await window.wails!.CallByName('api.ListAccounts')) as unknown[]
  },
}

export const client: Client = window.wails ? wailsClient : httpClient
