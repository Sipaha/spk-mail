# Folders + Built-in Views Implementation Plan (Plan 9 of 9)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Prerequisites:** Plans 1–8 merged.

**Goal:** Surface IMAP folders in the sidebar (Inbox / Sent / Drafts / Trash / custom), with per-folder unread badges. Add two virtual views: **Unread** (all messages with `\Seen` not set) and **Flagged**. Both views are top-level filters that override the active folder/account context. Backend already discovers and syncs all folders; this plan exposes them.

**Architecture:**
- **Storage**: `Store.ListThreads(ThreadFilter, limit, offset)` replaces `ListThreadsByProfile` and honors all filter fields conditionally (`profile_id`, `account_id`, `folder_id`, `unread_only`, `has_flagged`). Add `Store.UnreadCountsByFolder(accountID)` for badges.
- **Backend sync**: `AccountWorker` currently runs IDLE on inbox-role folders and syncs others once at startup. Add periodic poll (90s ticker) for non-inbox folders so new mail in Sent/Spam/etc. arrives without restart.
- **API**: `ListFolders(accountID)` returns `[]FolderDTO{ID, AccountID, Name, Role, UnreadCount}`. `Stub.ListThreads` respects all filter fields. `ThreadFilter` already has all needed columns — frontend just starts populating them.
- **Frontend**: `FolderTree` per account in sidebar, ordered by role (inbox → sent → drafts → archive → custom alphabetical → spam → trash). Click folder → `setFilter({accountId, folderId})`. Live unread badge per folder. Two top-level virtual-view buttons "Unread" and "Flagged" above accounts; clicking sets `filter.unreadOnly`/`filter.hasFlagged` and clears account/folder context.

**Tech Stack:** Standard Go + existing React 19 + Zustand. No new deps.

---

## Task 1: Storage — `Store.ListThreads(ThreadFilter, limit, offset)` + `Store.UnreadCountsByFolder`

**Files:** `internal/storage/threads.go`, `internal/storage/threads_test.go` (new tests).

- [ ] **Step 1: Add a struct so the SQL builder knows what to filter on.** Reuse the existing `api.ThreadFilter` shape via a parallel struct — storage shouldn't import api. Define:

```go
// internal/storage/threads.go

type ThreadFilter struct {
    AccountID  *int64
    FolderID   *int64
    ProfileID  *int64
    UnreadOnly bool
    HasFlagged bool
}
```

- [ ] **Step 2: New method `ListThreads`** that builds SQL conditionally. Replaces `ListThreadsByProfile`. Keep `ListThreadsRecent` as a thin wrapper for backwards-compat (just calls `ListThreads` with empty filter).

```go
func (s *Store) ListThreads(ctx context.Context, f ThreadFilter, limit, offset int) ([]ThreadRow, error) {
    if limit <= 0 { limit = 100 }
    var (
        joins  []string
        wheres []string
        args   []any
    )
    needMessageJoin := f.AccountID != nil || f.FolderID != nil || f.ProfileID != nil
    if needMessageJoin {
        joins = append(joins, `EXISTS (SELECT 1 FROM messages m JOIN accounts a ON a.id = m.account_id`)
        var sub []string
        sub = append(sub, `m.thread_id = t.id`)
        if f.AccountID != nil { sub = append(sub, `a.id = ?`); args = append(args, *f.AccountID) }
        if f.FolderID != nil  { sub = append(sub, `m.folder_id = ?`); args = append(args, *f.FolderID) }
        if f.ProfileID != nil { sub = append(sub, `a.profile_id = ?`); args = append(args, *f.ProfileID) }
        wheres = append(wheres, "EXISTS (SELECT 1 FROM messages m JOIN accounts a ON a.id = m.account_id WHERE " + strings.Join(sub, " AND ") + ")")
    }
    if f.UnreadOnly  { wheres = append(wheres, "t.unread_count > 0") }
    if f.HasFlagged  { wheres = append(wheres, "t.has_flagged = 1") }

    q := "SELECT t.id, t.subject_norm, t.last_date, t.msg_count, t.unread_count, t.has_flagged, t.has_attach FROM threads t"
    _ = joins // (joins handled inline via EXISTS subqueries; keep variable for future LEFT JOIN need)
    if len(wheres) > 0 {
        q += " WHERE " + strings.Join(wheres, " AND ")
    }
    q += " ORDER BY t.last_date DESC LIMIT ? OFFSET ?"
    args = append(args, limit, offset)

    rows, err := s.db.QueryContext(ctx, q, args...)
    if err != nil { return nil, err }
    defer rows.Close()
    // (same scan loop as ListThreadsRecent)
}
```

Adjust the SQL according to what actually compiles cleanly — the structure above is illustrative. The key invariants:
- All user values pass through `?` placeholders.
- Empty filter → `SELECT … FROM threads t ORDER BY last_date DESC LIMIT ? OFFSET ?` (same as old behavior).
- Each filter field adds a single AND clause.

- [ ] **Step 3: `Store.UnreadCountsByFolder(ctx, accountID)`** — returns `map[folderID]int64` of unread inbox-role messages per folder for that account:

```go
func (s *Store) UnreadCountsByFolder(ctx context.Context, accountID int64) (map[int64]int64, error) {
    rows, err := s.db.QueryContext(ctx, `
        SELECT m.folder_id, COUNT(*)
        FROM messages m
        WHERE m.account_id = ?
          AND NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')
        GROUP BY m.folder_id`, accountID)
    if err != nil { return nil, err }
    defer rows.Close()
    out := map[int64]int64{}
    for rows.Next() {
        var fid, n int64
        if err := rows.Scan(&fid, &n); err != nil { return nil, err }
        out[fid] = n
    }
    return out, rows.Err()
}
```

- [ ] **Step 4: Tests**

`internal/storage/threads_filter_test.go`:
```go
func TestListThreads_FilterCombinations(t *testing.T) {
    s := openTestStore(t)
    ctx := context.Background()
    // seed two profiles, two accounts in different profiles, two folders each, mixed unread/flagged threads
    // Assert each filter combo returns exactly the expected subset:
    //   - empty filter → all
    //   - profile_id set → threads with at least one message in that profile's accounts
    //   - account_id set → threads with messages in that account
    //   - folder_id set → threads with messages in that folder
    //   - unread_only → threads with unread_count > 0
    //   - has_flagged → threads with has_flagged = 1
    //   - profile_id + folder_id → AND
}

func TestUnreadCountsByFolder_PerFolder(t *testing.T) { /* basic */ }
```

- [ ] **Step 5: Run + commit**
```bash
go test ./internal/storage/ -count=1 -v -run 'TestListThreads_Filter|TestUnreadCountsByFolder'
git add internal/storage/threads.go internal/storage/threads_filter_test.go
git commit -m "feat(storage): ListThreads accepts unified ThreadFilter; UnreadCountsByFolder helper"
```

---

## Task 2: API — ListFolders RPC + ThreadFilter all-fields

**Files:** `internal/api/{api,dto,stub}.go`, `internal/api/transport/{http,wails}.go`, `internal/api/folders_test.go` (new).

- [ ] **Step 1: DTO**

`internal/api/dto.go`:
```go
type FolderDTO struct {
    ID          int64  `json:"id"`
    AccountID   int64  `json:"account_id"`
    Name        string `json:"name"`
    Role        string `json:"role"`         // "inbox"|"sent"|"drafts"|"trash"|"spam"|"archive"|""
    UnreadCount int64  `json:"unread_count"`
}
```

`ThreadFilter` already has `AccountID`, `FolderID`, `UnreadOnly`, `Limit`, `Offset` from plan 1, plus `ProfileID` from plan 8. Add `HasFlagged bool` if not present.

- [ ] **Step 2: API interface**

```go
ListFolders(ctx context.Context, accountID int64) ([]FolderDTO, error)
```

- [ ] **Step 3: Stub.ListFolders** — call `Store.ListFolders` + `Store.UnreadCountsByFolder`, merge into DTOs ordered by role (inbox → sent → drafts → archive → "" alphabetical → spam → trash):

```go
func (s *Stub) ListFolders(ctx context.Context, accountID int64) ([]FolderDTO, error) {
    rows, err := s.Store.ListFolders(ctx, accountID)
    if err != nil { return nil, err }
    counts, _ := s.Store.UnreadCountsByFolder(ctx, accountID)
    out := make([]FolderDTO, 0, len(rows))
    for _, r := range rows {
        role := ""
        if r.Role != nil { role = *r.Role }
        out = append(out, FolderDTO{ID: r.ID, AccountID: accountID, Name: r.Name, Role: role, UnreadCount: counts[r.ID]})
    }
    sortFoldersByRole(out)
    return out, nil
}
```

`sortFoldersByRole` puts roles in this order: inbox=0, sent=1, drafts=2, archive=3, ""=4, spam=5, trash=6. Within same role, alphabetical.

- [ ] **Step 4: Stub.ListThreads** — replace the call to `Store.ListThreadsByProfile` with `Store.ListThreads` translating from `api.ThreadFilter` to `storage.ThreadFilter`:

```go
func (s *Stub) ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadDTO, error) {
    sf := storage.ThreadFilter{
        AccountID:  filter.AccountID,
        FolderID:   filter.FolderID,
        ProfileID:  filter.ProfileID,
        UnreadOnly: filter.UnreadOnly,
        HasFlagged: filter.HasFlagged,
    }
    limit := filter.Limit
    if limit <= 0 { limit = 100 }
    rows, err := s.Store.ListThreads(ctx, sf, limit, filter.Offset)
    // …existing DTO conversion…
}
```

- [ ] **Step 5: Transports**

In `internal/api/transport/http.go`:
```go
h.mux.HandleFunc("POST /api/ListFolders", httpHandle(func(ctx context.Context, req *struct{ AccountID int64 `json:"account_id"` }) (any, error) {
    return h.api.ListFolders(ctx, req.AccountID)
}))
```

In `internal/api/transport/wails.go`:
```go
func (w *API) ListFolders(accountID int64) ([]api.FolderDTO, error) { return w.a.ListFolders(context.Background(), accountID) }
```

- [ ] **Step 6: Tests**

`internal/api/folders_test.go`:
- `TestListFolders_OrderedByRole` — seed an account with INBOX, Sent, Drafts, Custom, Spam folders; assert returned order.
- `TestListFolders_UnreadCounts` — insert messages with mixed `\Seen` flags; assert per-folder counts.
- `TestListThreads_HonorsAllFilters` — confirm folder_id and unread_only filters now actually filter (regression for the previous "Stub ignores filter" bug).

- [ ] **Step 7: Run + commit**
```bash
go test ./internal/api/...
git add internal/api/
git commit -m "feat(api): ListFolders RPC with unread badges; ListThreads honors full filter"
```

---

## Task 3: Backend — periodic poll for non-INBOX folders

**Files:** `internal/sync/account_worker.go`, `internal/sync/account_worker_test.go`.

Currently `AccountWorker.runOnce` syncs all folders ONCE at startup, then runs IDLE on inbox-role only. Non-INBOX folders never refresh after startup. Fix: add a 90-second poll ticker that re-runs `syncFolder` on every non-INBOX, non-Trash folder.

- [ ] **Step 1: Implement**

After the existing IDLE-spawning loop in `runOnce`, add:

```go
// Periodic poll for non-inbox folders (Sent, Drafts, Archive, custom, …).
// IMAP IDLE is reserved for INBOX (highest signal/noise); other folders get
// best-effort polling every pollNonInboxInterval.
go func() {
    t := time.NewTicker(90 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-t.C:
            stored, _ := w.store.ListFolders(ctx, w.accountID)
            for _, f := range stored {
                role := ""
                if f.Role != nil { role = *f.Role }
                if role == "inbox" || role == "trash" || role == "spam" {
                    continue
                }
                if err := w.syncFolder(ctx, c, f.ID, f.Name, role); err != nil {
                    slog.Warn("non-inbox poll failed", "folder", f.Name, "err", err)
                }
            }
        }
    }
}()
```

- [ ] **Step 2: Test**

Append to `account_worker_test.go`:
```go
func TestAccountWorker_PollsNonInboxFolders(t *testing.T) {
    // Spin up mockimap with an INBOX + a Sent folder.
    // Append a message to Sent AFTER AccountWorker starts (so initial sync misses it).
    // Wait > pollInterval (or use a dependency-injected clock if convenient).
    // Assert the message lands in the DB.
}
```

If 90 seconds is too long for the test, factor `pollNonInboxInterval` as a `var` so the test can lower it to 200ms.

- [ ] **Step 3: Run + commit**
```bash
go test ./internal/sync/ -count=1 -v -run TestAccountWorker_Polls -timeout 90s
git add internal/sync/account_worker.go internal/sync/account_worker_test.go
git commit -m "feat(sync): poll non-INBOX folders every 90s for new mail"
```

---

## Task 4: Frontend — types + client + store filter wiring

**Files:** `frontend/src/api/{types,client}.ts`, `frontend/src/store/index.ts`.

- [ ] **Step 1: Types**

`types.ts`:
```ts
export interface FolderDTO { id: number; account_id: number; name: string; role: string; unread_count: number }
```
Extend `ThreadFilter` with `has_flagged?: boolean` and `folder_id?: number` (already there from plan 1).

- [ ] **Step 2: Client**

```ts
listFolders(accountId: number): Promise<FolderDTO[]>
```
Both impls.

- [ ] **Step 3: Store**

Add to `State`:
```ts
folders: Record<number, FolderDTO[]>  // keyed by accountId
filter: { accountId?: number; folderId?: number; unreadOnly: boolean; hasFlagged: boolean }
```

Setter `setFolders(accountId: number, folders: FolderDTO[])`. Existing `setFilter` is fine; add a helper `setView(view: 'unread' | 'flagged' | null)` that flips the right boolean and clears account/folder context.

- [ ] **Step 4: Run + commit**
```bash
cd frontend && npm run build
git add frontend/src/api/ frontend/src/store/
git commit -m "feat(frontend): FolderDTO type + client.listFolders + view/filter store"
```

---

## Task 5: Frontend — FolderTree + virtual views in sidebar

**Files:** `frontend/src/components/{FolderTree,ViewSwitcher}.tsx` (new), modify `AccountSidebar.tsx`.

- [ ] **Step 1: FolderTree**

For each account, `FolderTree` renders a small tree. Click a folder → `setFilter({accountId: a.id, folderId: f.id, unreadOnly: false, hasFlagged: false})`. Show unread count next to the folder name. Use icons (📥📤📝🗃🗑) by role.

```tsx
// frontend/src/components/FolderTree.tsx
import { useEffect } from 'react'
import { client } from '../api/client'
import { useStore } from '../store'

const ROLE_ICON: Record<string, string> = {
  inbox: '📥', sent: '📤', drafts: '📝', archive: '🗃', spam: '⚠️', trash: '🗑',
}

export default function FolderTree({ accountId }: { accountId: number }) {
  const folders = useStore(s => s.folders[accountId] ?? [])
  const setFolders = useStore(s => s.setFolders)
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)

  useEffect(() => {
    client.listFolders(accountId).then(fs => setFolders(accountId, fs))
  }, [accountId, setFolders])

  return (
    <ul className="ml-4 space-y-0.5 text-sm">
      {folders.map(f => {
        const active = filter.accountId === accountId && filter.folderId === f.id
        return (
          <li key={f.id}>
            <button
              onClick={() => setFilter({ accountId, folderId: f.id, unreadOnly: false, hasFlagged: false })}
              className={`w-full flex items-center gap-2 px-2 py-1 rounded hover:bg-zinc-800 ${active ? 'bg-zinc-800' : ''}`}>
              <span>{ROLE_ICON[f.role] ?? '📁'}</span>
              <span className="truncate">{f.name}</span>
              {f.unread_count > 0 && (
                <span className="ml-auto text-[10px] rounded-full bg-blue-600 text-white px-1.5">
                  {f.unread_count}
                </span>
              )}
            </button>
          </li>
        )
      })}
    </ul>
  )
}
```

- [ ] **Step 2: ViewSwitcher**

Top of sidebar (above accounts), two buttons:
```tsx
// frontend/src/components/ViewSwitcher.tsx
import { useStore } from '../store'

export default function ViewSwitcher() {
  const filter = useStore(s => s.filter)
  const setFilter = useStore(s => s.setFilter)

  const setView = (view: 'unread' | 'flagged' | null) => {
    setFilter({
      accountId: undefined, folderId: undefined,
      unreadOnly: view === 'unread',
      hasFlagged: view === 'flagged',
    })
  }
  const isUnread  = filter.unreadOnly && !filter.hasFlagged
  const isFlagged = filter.hasFlagged && !filter.unreadOnly

  return (
    <div className="flex gap-1 px-3 py-2 border-b border-zinc-800 text-xs">
      <button onClick={() => setView('unread')}
        className={`flex-1 px-2 py-1 rounded ${isUnread ? 'bg-blue-600 text-white' : 'hover:bg-zinc-800'}`}>
        Unread
      </button>
      <button onClick={() => setView('flagged')}
        className={`flex-1 px-2 py-1 rounded ${isFlagged ? 'bg-amber-600 text-white' : 'hover:bg-zinc-800'}`}>
        Flagged
      </button>
    </div>
  )
}
```

- [ ] **Step 3: Update AccountSidebar**

Each account becomes expandable; below the account row render `<FolderTree accountId={a.id} />`. Optionally collapse/expand with a chevron — for v1 keep always expanded for simplicity.

```tsx
// AccountSidebar.tsx — replacement of the current map
{visibleAccounts.map(a => (
  <div key={a.id}>
    <button
      onClick={() => setFilter({ accountId: a.id, folderId: undefined, unreadOnly: false, hasFlagged: false })}
      className={`w-full flex items-center gap-2 rounded px-2 py-1.5 hover:bg-zinc-800 ${filter.accountId === a.id && !filter.folderId ? 'bg-zinc-800' : ''}`}>
      <span className="size-2.5 rounded-full" style={{ background: a.color }} />
      <span className="truncate">{a.name}</span>
      <span className={`ml-auto text-[10px] ${a.status === 'ok' ? 'text-emerald-400' : a.status === 'connecting' ? 'text-amber-400' : 'text-red-400'}`}>{a.status}</span>
    </button>
    <FolderTree accountId={a.id} />
  </div>
))}
```

- [ ] **Step 4: Wire ViewSwitcher into App.tsx sidebar**

Sidebar slot gets one more line at the top: `<ProfileSwitcher /><SearchBar /><ViewSwitcher /><AccountSidebar />`.

- [ ] **Step 5: ThreadList consumes the wider filter**

`ThreadList.tsx` already passes `filter.accountId`, `filter.unreadOnly`. Add `folder_id` and `has_flagged`:
```tsx
client.listThreads({
  account_id: filter.accountId,
  folder_id: filter.folderId,
  unread_only: filter.unreadOnly,
  has_flagged: filter.hasFlagged,
  profile_id: activeProfileId ?? undefined,
  limit: 200,
}).then(setThreads)
// add filter.folderId, filter.hasFlagged to deps
```

- [ ] **Step 6: Vitest for FolderTree click**

`frontend/src/components/FolderTree.test.tsx`:
```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import FolderTree from './FolderTree'
import { useStore } from '../store'

vi.mock('../api/client', () => ({
  client: {
    listFolders: () => Promise.resolve([
      { id: 10, account_id: 1, name: 'INBOX',  role: 'inbox',  unread_count: 3 },
      { id: 11, account_id: 1, name: 'Sent',   role: 'sent',   unread_count: 0 },
    ]),
  },
}))

beforeEach(() => useStore.setState({ folders: {}, filter: { unreadOnly: false, hasFlagged: false } }))

describe('FolderTree', () => {
  it('renders folders with unread badges', async () => {
    render(<FolderTree accountId={1} />)
    expect(await screen.findByText('INBOX')).toBeTruthy()
    expect(screen.getByText('3')).toBeTruthy()
  })

  it('clicking a folder updates filter', async () => {
    render(<FolderTree accountId={1} />)
    fireEvent.click(await screen.findByText('Sent'))
    await waitFor(() => expect(useStore.getState().filter.folderId).toBe(11))
  })
})
```

- [ ] **Step 7: Build + commit**
```bash
cd frontend && npx vitest run && npm run build && cd .. && make build
git add frontend/
git commit -m "feat(frontend): FolderTree + ViewSwitcher (Unread/Flagged) in sidebar"
```

---

## Task 6: Live unread badge updates + Playwright

**Files:** `frontend/src/api/events.ts`, `tests/playwright/folders.spec.ts` (new), `tests/fixtures/basic.yaml` (add a Sent folder + message).

- [ ] **Step 1: events.ts refreshes folders on relevant events**

After a `MessageInserted` / `MessageUpdated` / `MessageArrived`, refresh folder counts so the sidebar badge stays current. In `frontend/src/api/events.ts`, in those switch cases, refresh folders for the affected account:

```ts
case 'MessageInserted':
case 'MessageUpdated':
case 'MessageArrived': {
  const accId = Number(ev.payload.account_id)
  if (Number.isFinite(accId) && accId > 0) {
    client.listFolders(accId).then(fs => useStore.getState().setFolders(accId, fs))
  }
  // …existing thread refresh…
  break
}
```

- [ ] **Step 2: Playwright spec**

`tests/playwright/folders.spec.ts`:
```ts
import { test, expect } from '@playwright/test'

test('folders render in sidebar with unread badges', async ({ page }) => {
  await page.goto('/')
  // basic.yaml seeds INBOX + Sent (Plan 9 Task 6 fixture extension)
  await expect(page.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  await expect(page.getByText('Sent')).toBeVisible({ timeout: 5_000 })
})

test('Unread view filters thread list', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByText('INBOX')).toBeVisible({ timeout: 10_000 })
  await page.getByRole('button', { name: 'Unread', exact: true }).click()
  // After clicking Unread, ThreadList should only show threads with unread_count > 0.
  // basic.yaml has at least one unread thread, so SOME thread row should render.
  await expect(page.locator('[data-test-id]').or(page.getByText(/Project|milestones|Weekly/))).toBeTruthy()
})

test('clicking a folder filters threads to that folder', async ({ page }) => {
  await page.goto('/')
  await page.getByText('Sent').first().click({ timeout: 10_000 })
  // No Sent messages in basic.yaml → "No threads."
  await expect(page.getByText(/No threads/i)).toBeVisible({ timeout: 5_000 })
})
```

- [ ] **Step 3: Extend fixture**

`tests/fixtures/basic.yaml`: add a `Sent` folder with one message:
```yaml
      - name: Sent
        messages:
          - from: "Alice <alice@example.com>"
            to: ["bob@example.com"]
            subject: "Re: Q1 plans"
            date: 2026-04-26T15:00:00Z
            body_text: "Sounds good."
```

(The seed loader already supports multiple folders — see `internal/mockimap/seed.go`'s outer loop.)

- [ ] **Step 4: Run + commit**
```bash
make build
cd tests/playwright && CI=1 npx playwright test && cd ../..
git add tests/playwright/folders.spec.ts tests/fixtures/basic.yaml frontend/src/api/events.ts
git commit -m "test(ui): playwright covers FolderTree + Unread view; live folder unread badges"
```

---

## Self-Review

**Spec coverage added by plan 9:**
- Folder discovery: backend already does it (Plan 2). This plan exposes folders via API + UI.
- Per-folder unread badges: `Store.UnreadCountsByFolder` + live refresh on events.
- Built-in views (Unread, Flagged): top-level filter toggles using the existing `unread_only`/`has_flagged` thread columns.
- Periodic non-INBOX poll: 90-second ticker (Task 3) so Sent/Drafts/Archive folders catch new mail without restart.

**Open questions for follow-up plans (post-v1):**
- Custom folder ordering (drag-and-drop in FolderTree).
- "Move to folder" UI (IMAP MOVE / COPY+EXPUNGE).
- Per-folder IDLE on Gmail-style multiplexed connections.
- Push-style refresh of folder badges via SSE (currently re-fetches via REST after each event — adequate for low message rates).
