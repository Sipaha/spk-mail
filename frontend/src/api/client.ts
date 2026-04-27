import { Call, Events } from '@wailsio/runtime'
import type { AccountDTO, AddAccountRequest, AddProfileRequest, ApiEvent, EventType, MessageDTO, ProfileDTO, SearchHitDTO, ThreadDTO, ThreadFilter, UpdateProfileRequest } from './types'

export interface Client {
  listAccounts(): Promise<AccountDTO[]>
  addAccount(req: AddAccountRequest): Promise<AccountDTO>
  removeAccount(id: number): Promise<void>
  listThreads(filter: ThreadFilter): Promise<ThreadDTO[]>
  getThread(id: number): Promise<MessageDTO[]>
  markRead(ids: number[]): Promise<void>
  allowRemote(id: number): Promise<string>
  search(query: string, limit: number, offset: number): Promise<SearchHitDTO[]>
  openAttachment(id: number): Promise<void>
  listProfiles(): Promise<ProfileDTO[]>
  addProfile(req: AddProfileRequest): Promise<ProfileDTO>
  updateProfile(req: UpdateProfileRequest): Promise<ProfileDTO>
  deleteProfile(id: number): Promise<void>
  setProfileMuted(id: number, muted: boolean): Promise<void>
  subscribeEvents(onEvent: (e: ApiEvent) => void): () => void
}

const post = async <T,>(path: string, body: unknown): Promise<T> => {
  const r = await fetch(path, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body ?? {}) })
  if (!r.ok) {
    const txt = (await r.text()).trim()
    throw new Error(txt || `HTTP ${r.status} ${r.statusText} — ${path}`)
  }
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
  openAttachment: (id) => post('/api/OpenAttachment', { id }),
  listProfiles:  () => post('/api/ListProfiles', {}),
  addProfile:    (req) => post('/api/AddProfile', req),
  updateProfile: (req) => post('/api/UpdateProfile', req),
  deleteProfile: (id) => post('/api/DeleteProfile', { id }).then(() => undefined),
  setProfileMuted: (id, muted) => post('/api/SetProfileMuted', { id, muted }).then(() => undefined),
  subscribeEvents: (onEvent) => {
    const es = new EventSource('/api/events')
    const handler = (ev: MessageEvent) => {
      try {
        onEvent(JSON.parse(ev.data) as ApiEvent)
      } catch (err) {
        console.warn('SSE parse failed', err, ev.data)
      }
    }
    ;(['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError','AttachmentReady'] as const).forEach(t => es.addEventListener(t, handler))
    es.onerror = (err) => {
      console.warn('SSE error', err, 'readyState', es.readyState)
    }
    return () => es.close()
  },
}

// Wails v3 alpha exposes bindings via @wailsio/runtime. Method names are
// fully-qualified with the bound service name (see internal/api/transport
// /wails.go — the service is registered as application.NewService(svc) so
// methods live under the struct's package-qualified name "api.<Method>").
const wailsClient: Client = {
  listAccounts:  () => Call.ByName('api.ListAccounts') as Promise<AccountDTO[]>,
  addAccount:    (req) => Call.ByName('api.AddAccount', req) as Promise<AccountDTO>,
  removeAccount: (id) => Call.ByName('api.RemoveAccount', id).then(() => undefined),
  listThreads:   (f) => Call.ByName('api.ListThreads', f) as Promise<ThreadDTO[]>,
  getThread:     (id) => Call.ByName('api.GetThread', id) as Promise<MessageDTO[]>,
  markRead:      (ids) => Call.ByName('api.MarkRead', ids).then(() => undefined),
  allowRemote:   (id) => Call.ByName('api.AllowRemoteForMessage', id) as Promise<string>,
  search:        (q, l, o) => Call.ByName('api.Search', q, l, o) as Promise<SearchHitDTO[]>,
  openAttachment: (id) => Call.ByName('api.OpenAttachment', id).then(() => undefined),
  listProfiles:  () => Call.ByName('api.ListProfiles') as Promise<ProfileDTO[]>,
  addProfile:    (req) => Call.ByName('api.AddProfile', req) as Promise<ProfileDTO>,
  updateProfile: (req) => Call.ByName('api.UpdateProfile', req) as Promise<ProfileDTO>,
  deleteProfile: (id) => Call.ByName('api.DeleteProfile', id).then(() => undefined),
  setProfileMuted: (id, muted) => Call.ByName('api.SetProfileMuted', id, muted).then(() => undefined),
  subscribeEvents: (onEvent) => {
    const types: readonly EventType[] = ['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError','AttachmentReady'] as const
    const offs = types.map(t => Events.On(t, (ev) => onEvent({
      type: t,
      payload: ((ev?.data as Record<string, unknown> | undefined) ?? {}),
    })))
    return () => offs.forEach(off => { if (typeof off === 'function') off() })
  },
}

/**
 * Wails v3 alpha serves the embedded React bundle from `wails://localhost/`.
 * There is no `window.wails` global anymore — that was the v2 convention —
 * so we sniff the URL protocol instead. In dev / browser mode the protocol
 * is `http:` or `https:` and we fall back to the JSON-over-fetch transport
 * served by internal/api/transport/http.go.
 */
const isWails = typeof window !== 'undefined' && window.location.protocol === 'wails:'
export const client: Client = isWails ? wailsClient : httpClient
