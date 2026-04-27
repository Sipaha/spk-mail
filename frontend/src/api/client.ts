import type { AccountDTO, AddAccountRequest, ApiEvent, MessageDTO, ThreadDTO, ThreadFilter } from './types'

export interface Client {
  listAccounts(): Promise<AccountDTO[]>
  addAccount(req: AddAccountRequest): Promise<AccountDTO>
  removeAccount(id: number): Promise<void>
  listThreads(filter: ThreadFilter): Promise<ThreadDTO[]>
  getThread(id: number): Promise<MessageDTO[]>
  markRead(ids: number[]): Promise<void>
  allowRemote(id: number): Promise<string>
  search(query: string, limit: number, offset: number): Promise<MessageDTO[]>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

type Wails = { CallByName: (name: string, ...args: unknown[]) => Promise<unknown>; EventsOn?: (name: string, cb: (data: unknown) => void) => () => void }
declare global { interface Window { wails?: Wails } }

const post = async <T,>(path: string, body: unknown): Promise<T> => {
  const r = await fetch(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body ?? {}) })
  if (!r.ok) throw new Error(await r.text())
  if (r.headers.get('content-type')?.includes('application/json')) return r.json() as Promise<T>
  return undefined as unknown as T
}

const httpClient: Client = {
  listAccounts:  () => post('/api/ListAccounts', {}),
  addAccount:    (req) => post('/api/AddAccount', req),
  removeAccount: (id) => post('/api/RemoveAccount', { id }),
  listThreads:   (f) => post('/api/ListThreads', f),
  getThread:     (id) => post('/api/GetThread', { id }),
  markRead:      (ids) => post('/api/MarkRead', { ids }),
  allowRemote:   (id) => post('/api/AllowRemoteForMessage', { id }),
  search:        (query, limit, offset) => post('/api/Search', { query, limit, offset }),
  subscribeEvents: (onEvent) => {
    const es = new EventSource('/api/events')
    const handler = (ev: MessageEvent) => {
      try {
        onEvent(JSON.parse(ev.data) as ApiEvent)
      } catch (err) {
        console.warn('SSE parse failed', err, ev.data)
      }
    }
    ;(['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError'] as const).forEach(t => es.addEventListener(t, handler))
    es.onerror = (err) => {
      console.warn('SSE error', err, 'readyState', es.readyState)
    }
    return () => es.close()
  },
}

const wailsClient: Client = {
  listAccounts:  () => window.wails!.CallByName('api.ListAccounts') as Promise<AccountDTO[]>,
  addAccount:    (req) => window.wails!.CallByName('api.AddAccount', req) as Promise<AccountDTO>,
  removeAccount: (id) => window.wails!.CallByName('api.RemoveAccount', id).then(() => undefined),
  listThreads:   (f) => window.wails!.CallByName('api.ListThreads', f) as Promise<ThreadDTO[]>,
  getThread:     (id) => window.wails!.CallByName('api.GetThread', id) as Promise<MessageDTO[]>,
  markRead:      (ids) => window.wails!.CallByName('api.MarkRead', ids).then(() => undefined),
  allowRemote:   (id) => window.wails!.CallByName('api.AllowRemoteForMessage', id) as Promise<string>,
  search:        (q, l, o) => window.wails!.CallByName('api.Search', q, l, o) as Promise<MessageDTO[]>,
  subscribeEvents: (onEvent) => {
    if (!window.wails?.EventsOn) return () => {}
    const offs = (['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError'] as const)
      .map(t => window.wails!.EventsOn!(t, (data: unknown) => onEvent({ type: t, payload: (data as Record<string, unknown>) ?? {} })))
    return () => offs.forEach(off => off())
  },
}

/**
 * Selected once at module load. The Wails runtime must be injected before
 * the first import of this module — typically guaranteed by the v3 bundle
 * order, but if you see desktop builds falling back to fetch, check inject
 * order.
 */
export const client: Client = window.wails ? wailsClient : httpClient
