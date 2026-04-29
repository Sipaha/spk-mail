import { Call, Events } from '@wailsio/runtime'
import type { AccountDTO, AddAccountRequest, AddProfileRequest, ApiEvent, EventType, FolderDTO, MessageDTO, ProfileDTO, SearchHitDTO, ThreadDTO, ThreadFilter, UpdateProfileRequest } from './types'

export interface Client {
  listAccounts(): Promise<AccountDTO[]>
  addAccount(req: AddAccountRequest): Promise<AccountDTO>
  removeAccount(id: number): Promise<void>
  listThreads(filter: ThreadFilter): Promise<ThreadDTO[]>
  listFolders(accountId: number): Promise<FolderDTO[]>
  getThread(id: number): Promise<MessageDTO[]>
  markRead(ids: number[]): Promise<void>
  markFolderRead(folderId: number): Promise<number>
  toggleThreadFlagged(threadId: number): Promise<{ action: string; count: number }>
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
  listFolders:   (accountId) => post('/api/ListFolders', { account_id: accountId }),
  getThread:     (id) => post('/api/GetThread', { id }),
  markRead:      (ids) => post('/api/MarkRead', { ids }),
  markFolderRead: (folderId) => post<{count: number}>('/api/MarkFolderRead', { folder_id: folderId }).then(r => r.count),
  toggleThreadFlagged: (threadId) =>
    post<{ action: string; count: number }>('/api/ToggleThreadFlagged', { thread_id: threadId }),
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
    ;(['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError','AttachmentReady','FolderMarkedRead'] as const).forEach(t => es.addEventListener(t, handler))
    es.onerror = (err) => {
      console.warn('SSE error', err, 'readyState', es.readyState)
    }
    return () => es.close()
  },
}

// Wails v3 alpha exposes bindings via @wailsio/runtime. Call.ByName takes the
// fully-qualified Go method name: <package-import-path>.<TypeName>.<MethodName>.
// Wails computes that FQN with reflect.Type.PkgPath() + "." + Type.Name() +
// "." + methodName (see pkg/application/bindings.go in wails v3) and uses it
// as the lookup key in its bound-methods map. ServiceOptions.Name only
// affects logging — it is NOT used for binding lookup, so we can't shorten
// the prefix. The prefix below MUST match the package + struct in
// internal/api/transport/wails.go (`type API` in package transport, located
// at github.com/spk/spk-mail/internal/api/transport).
const BIND_NS = 'github.com/spk/spk-mail/internal/api/transport.API'
const m = (method: string) => `${BIND_NS}.${method}`

const wailsClient: Client = {
  listAccounts:  () => Call.ByName(m('ListAccounts')) as Promise<AccountDTO[]>,
  addAccount:    (req) => Call.ByName(m('AddAccount'), req) as Promise<AccountDTO>,
  removeAccount: (id) => Call.ByName(m('RemoveAccount'), id).then(() => undefined),
  listThreads:   (f) => Call.ByName(m('ListThreads'), f) as Promise<ThreadDTO[]>,
  listFolders:   (accountId) => Call.ByName(m('ListFolders'), accountId) as Promise<FolderDTO[]>,
  getThread:     (id) => Call.ByName(m('GetThread'), id) as Promise<MessageDTO[]>,
  markRead:      (ids) => Call.ByName(m('MarkRead'), ids).then(() => undefined),
  markFolderRead: (folderId) => Call.ByName(m('MarkFolderRead'), folderId) as Promise<number>,
  toggleThreadFlagged: (threadId) =>
    Call.ByName(m('ToggleThreadFlagged'), threadId) as Promise<{ action: string; count: number }>,
  allowRemote:   (id) => Call.ByName(m('AllowRemoteForMessage'), id) as Promise<string>,
  search:        (q, l, o) => Call.ByName(m('Search'), q, l, o) as Promise<SearchHitDTO[]>,
  openAttachment: (id) => Call.ByName(m('OpenAttachment'), id).then(() => undefined),
  listProfiles:  () => Call.ByName(m('ListProfiles')) as Promise<ProfileDTO[]>,
  addProfile:    (req) => Call.ByName(m('AddProfile'), req) as Promise<ProfileDTO>,
  updateProfile: (req) => Call.ByName(m('UpdateProfile'), req) as Promise<ProfileDTO>,
  deleteProfile: (id) => Call.ByName(m('DeleteProfile'), id).then(() => undefined),
  setProfileMuted: (id, muted) => Call.ByName(m('SetProfileMuted'), id, muted).then(() => undefined),
  subscribeEvents: (onEvent) => {
    const types: readonly EventType[] = ['MessageInserted','MessageArrived','MessageUpdated','SyncProgress','AccountStatus','WriteError','AttachmentReady','FolderMarkedRead'] as const
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
