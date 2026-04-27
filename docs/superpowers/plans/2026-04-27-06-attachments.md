# Attachments Implementation Plan (Plan 6 of 7)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisites:** Plans 1, 2, 3 merged.

**Goal:** Eagerly download attachment bodies in the background and let the user open them with the OS default application. Attachment metadata is already captured at insert time (plan 2). This plan adds a background downloader, file-system layout, an "open with system handler" pipeline, and the UI bits to show download status, click-to-open, and a re-download path when the local file is missing.

**Architecture:** A new `internal/sync/attachment_downloader.go` runs one goroutine per account that polls `attachments WHERE local_path IS NULL` ordered by `messages.date DESC`, fetches each via UID FETCH BODY[part_id] on a dedicated low-priority IMAP connection (so it doesn't block IDLE), writes atomically to `~/.local/share/spk-mail/attachments/<account_id>/<message_id>/<filename>`, hashes and updates the row. New API methods: `OpenAttachment(id)` shells out via `xdg-open`; `GetAttachmentLocalPath(id)` is for cases the frontend needs the path. UI: clickable attachment chips in `MessageBubble` with download progress, error state on missing files.

**Tech Stack:** Standard Go (`os/exec`, `crypto/sha256`). Reuses `internal/imap` and `internal/fsutil`. No new dependencies.

---

## File Structure

Created / modified:

```
internal/sync/
├── attachment_downloader.go      # background worker (per account)
└── attachment_downloader_test.go

internal/api/
├── api.go                        # +OpenAttachment, +GetAttachmentLocalPath
├── stub.go                       # implementations
└── transport/{http,wails}.go     # routes/methods

internal/storage/attachments.go    # +ListPendingAttachments, +UpdateAttachmentDownloaded, +ClearAttachmentLocalPath

internal/imap/fetch.go             # +FetchBodyPart(uid, partID) → []byte

frontend/src/
├── components/AttachmentChip.tsx  # icon + filename + click → open
├── api/types.ts                   # already has AttachmentDTO; +Downloaded already there
└── api/client.ts                  # +openAttachment

cmd/spk-mail/main.go               # start AttachmentDownloader workers alongside Engine
```

---

## Task 1: Storage helpers for the downloader

**Files:** modify `internal/storage/attachments.go`

- [ ] **Step 1: Tests**

`internal/storage/attachments_extra_test.go`:
```go
package storage

import (
	"context"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestAttachments_PendingThenMarkDownloaded(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name:"X",Email:"a@x",IMAPHost:"h",IMAPPort:993,IMAPUsername:"u",UseTLS:true,Color:"#fff",CreatedAt:0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter:"/", UIDValidity:1, UIDNext:1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	aID, _ := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1.2", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})

	pending, err := s.ListPendingAttachments(ctx, accID, 10)
	require.NoError(t, err); require.Len(t, pending, 1)
	require.Equal(t, "x.pdf", pending[0].Filename)

	require.NoError(t, s.UpdateAttachmentDownloaded(ctx, aID, "/tmp/x.pdf", "deadbeef", 1700000000))
	pending2, _ := s.ListPendingAttachments(ctx, accID, 10)
	require.Empty(t, pending2)
}
```

- [ ] **Step 2: Implement**

Add to `internal/storage/attachments.go`:
```go
type PendingAttachment struct {
	AttachmentID int64
	MessageID    int64
	AccountID    int64
	FolderID     int64
	UID          int64
	PartID       string
	Filename     string
	ContentType  string
	SizeBytes    int64
}

// ListPendingAttachments returns up to `limit` not-yet-downloaded attachments
// for the given account, newest message first.
func (s *Store) ListPendingAttachments(ctx context.Context, accountID int64, limit int) ([]PendingAttachment, error) {
	if limit <= 0 { limit = 50 }
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.message_id, m.account_id, m.folder_id, m.uid,
			a.part_id, a.filename, a.content_type, a.size_bytes
		FROM attachments a
		JOIN messages m ON m.id = a.message_id
		WHERE a.local_path IS NULL AND m.account_id = ?
		ORDER BY m.date DESC
		LIMIT ?`, accountID, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var out []PendingAttachment
	for rows.Next() {
		var p PendingAttachment
		if err := rows.Scan(&p.AttachmentID, &p.MessageID, &p.AccountID, &p.FolderID, &p.UID,
			&p.PartID, &p.Filename, &p.ContentType, &p.SizeBytes); err != nil { return nil, err }
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAttachmentDownloaded(ctx context.Context, id int64, localPath, sha256 string, ts int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE attachments SET local_path = ?, sha256 = ?, downloaded_at = ? WHERE id = ?`,
		localPath, sha256, ts, id)
	return err
}

func (s *Store) ClearAttachmentLocalPath(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE attachments SET local_path = NULL, downloaded_at = NULL WHERE id = ?`, id)
	return err
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/storage/ -v
git add internal/storage/
git commit -m "feat(storage): pending-attachments queue helpers"
```

---

## Task 2: IMAP body-part fetch

**Files:** modify `internal/imap/fetch.go`

- [ ] **Step 1: Implement**

Add to `internal/imap/fetch.go`:
```go
// FetchBodyPart fetches a single part of a message identified by IMAP
// BODYSTRUCTURE part path (e.g. "1.2") via UID FETCH BODY[<part>].
func (c *Client) FetchBodyPart(ctx context.Context, uid int64, partID string) ([]byte, error) {
	seq, _ := imap.ParseUIDSet(fmt.Sprintf("%d", uid))
	cmd := c.c.UIDFetch(seq, &imap.FetchOptions{
		BodySection: []*imap.FetchItemBodySection{ {Specifier: imap.PartSpecifierNone, Part: parsePartPath(partID), Peek: true} },
	})
	defer cmd.Close()
	var buf []byte
	for {
		msg := cmd.Next()
		if msg == nil { break }
		data, err := msg.Collect()
		if err != nil { return nil, err }
		for _, sec := range data.BodySection {
			if len(sec.Bytes) > 0 { buf = sec.Bytes; break }
		}
	}
	if err := cmd.Close(); err != nil { return nil, err }
	if buf == nil { return nil, fmt.Errorf("imap: no body part %q for uid %d", partID, uid) }
	return buf, nil
}

func parsePartPath(s string) []int {
	if s == "" { return nil }
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil { out = append(out, n) }
	}
	return out
}
```

(`import "strconv"`, `import "strings"` if missing.)

- [ ] **Step 2: Test**

Append to `internal/imap/client_test.go`:
```go
func TestClient_FetchBodyPart(t *testing.T) {
	mock, _ := mockimap.Start(context.Background(), "alice@example.com", "secret")
	defer mock.Close()
	host, port := splitHostPort(mock.Addr())

	// Append a multipart message with an attachment part
	u := mock.User("alice@example.com")
	mb, _ := u.Mailbox("INBOX")
	raw := []byte("From: x@y\r\nSubject: t\r\nMIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/octet-stream\r\n" +
		`Content-Disposition: attachment; filename="x.bin"` + "\r\n\r\n" +
		"PAYLOAD\r\n--b--\r\n")
	_, _ = mb.Append(nil, raw)

	c, err := Dial(context.Background(), DialOpts{Host: host, Port: port, Username: "alice@example.com", Password: "secret"})
	require.NoError(t, err); defer c.Close()
	_, _ = c.Select(context.Background(), "INBOX")

	body, err := c.FetchBodyPart(context.Background(), 1, "2")
	require.NoError(t, err)
	require.Contains(t, string(body), "PAYLOAD")
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/imap/ -v
git add internal/imap/
git commit -m "feat(imap): UID FETCH BODY[part] for attachment bodies"
```

---

## Task 3: AttachmentDownloader worker

**Files:** `internal/sync/attachment_downloader.go`, `internal/sync/attachment_downloader_test.go`

- [ ] **Step 1: Test (against mock IMAP + temp dir)**

`internal/sync/attachment_downloader_test.go`:
```go
package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestAttachmentDownloader_FetchesAndUpdatesRow(t *testing.T) {
	mock, _ := mockimap.Start(context.Background(), "alice@example.com", "secret")
	defer mock.Close()

	dir := t.TempDir()
	st, _ := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite")); defer st.Close()
	sec, _ := secrets.Open(filepath.Join(dir, "secrets.bin"), make([]byte, 32))

	host, port := imapSplitHostPort(mock.Addr())
	accID, _ := st.InsertAccount(context.Background(), storage.AccountRow{Name:"X",Email:"alice@example.com",IMAPHost:host,IMAPPort:port,IMAPUsername:"alice@example.com",UseTLS:false,Color:"#fff",CreatedAt:0})
	require.NoError(t, sec.Set(fmt.Sprintf("account:%d", accID), []byte("secret")))

	// Append message with attachment
	u := mock.User("alice@example.com")
	mb, _ := u.Mailbox("INBOX")
	raw := []byte("From: x@y\r\nSubject: t\r\nDate: Mon, 27 Apr 2026 10:30:00 +0000\r\nMessage-ID: <a@x>\r\nMIME-Version: 1.0\r\n" +
		`Content-Type: multipart/mixed; boundary="b"` + "\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"x.bin\"\r\n\r\nDATA\r\n--b--\r\n")
	_, _ = mb.Append(nil, raw)

	em := api.NewEmitter()
	writer := NewStoreWriter(st, em); go writer.Run(context.Background())
	w := NewAccountWorker(accID, st, sec, writer, em); go w.Run(context.Background())

	// Wait for the attachment row to be inserted
	deadline := time.Now().Add(3 * time.Second)
	var attID int64
	for time.Now().Before(deadline) {
		row := st.DB().QueryRow(`SELECT id FROM attachments LIMIT 1`)
		if err := row.Scan(&attID); err == nil { break }
		time.Sleep(50 * time.Millisecond)
	}
	require.NotZero(t, attID)

	// Run the downloader
	d := NewAttachmentDownloader(accID, st, sec, em, filepath.Join(dir, "attachments"))
	go d.Run(context.Background())

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var lp *string
		_ = st.DB().QueryRow(`SELECT local_path FROM attachments WHERE id = ?`, attID).Scan(&lp)
		if lp != nil {
			body, err := os.ReadFile(*lp)
			require.NoError(t, err)
			require.Contains(t, string(body), "DATA")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("attachment not downloaded within timeout")
}
```

- [ ] **Step 2: Implement**

`internal/sync/attachment_downloader.go`:
```go
package sync

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/imap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

type AttachmentDownloader struct {
	accountID  int64
	store      *storage.Store
	secrets    *secrets.Store
	em         *api.Emitter
	rootDir    string
}

func NewAttachmentDownloader(accountID int64, s *storage.Store, sec *secrets.Store, em *api.Emitter, rootDir string) *AttachmentDownloader {
	return &AttachmentDownloader{accountID: accountID, store: s, secrets: sec, em: em, rootDir: rootDir}
}

func (d *AttachmentDownloader) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done(): return
		case <-t.C: d.runOnce(ctx)
		}
	}
}

func (d *AttachmentDownloader) runOnce(ctx context.Context) {
	pending, err := d.store.ListPendingAttachments(ctx, d.accountID, 50)
	if err != nil || len(pending) == 0 { return }

	acc, err := d.store.GetAccount(ctx, d.accountID); if err != nil { return }
	pw, err := d.secrets.Get(fmt.Sprintf("account:%d", d.accountID)); if err != nil { return }

	c, err := imap.Dial(ctx, imap.DialOpts{Host: acc.IMAPHost, Port: acc.IMAPPort, Username: acc.IMAPUsername, Password: string(pw), UseTLS: acc.UseTLS})
	if err != nil { slog.Warn("downloader dial", "err", err); return }
	defer c.Close()

	folders, _ := d.store.ListFolders(ctx, d.accountID)
	folderName := func(id int64) string {
		for _, f := range folders { if f.ID == id { return f.Name } }
		return ""
	}

	// We may have pending attachments across multiple folders — group + SELECT once per folder
	currentFolder := ""
	for _, p := range pending {
		fname := folderName(p.FolderID); if fname == "" { continue }
		if fname != currentFolder {
			if _, err := c.Select(ctx, fname); err != nil { slog.Warn("select", "folder", fname, "err", err); continue }
			currentFolder = fname
		}
		body, err := c.FetchBodyPart(ctx, p.UID, p.PartID)
		if err != nil { slog.Warn("fetch part", "att", p.AttachmentID, "err", err); continue }
		path := filepath.Join(d.rootDir, strconv.FormatInt(d.accountID, 10), strconv.FormatInt(p.MessageID, 10), p.Filename)
		if err := fsutil.AtomicWrite(path, body, 0o600); err != nil { slog.Warn("write", "err", err); continue }
		sum, _ := fsutil.SHA256Reader(bytes.NewReader(body))
		if err := d.store.UpdateAttachmentDownloaded(ctx, p.AttachmentID, path, sum, time.Now().Unix()); err != nil { slog.Warn("update", "err", err); continue }
		d.em.Emit(api.Event{Type: "AttachmentReady", Payload: map[string]any{"attachment_id": p.AttachmentID, "message_id": p.MessageID}})
	}
}
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/sync/ -v -timeout 60s
git add internal/sync/attachment_downloader.go internal/sync/attachment_downloader_test.go
git commit -m "feat(sync): AttachmentDownloader background worker"
```

---

## Task 4: Wire downloader into Engine + main

**Files:** modify `internal/sync/engine.go`, `cmd/spk-mail/main.go`

- [ ] **Step 1: Engine**

Add to `internal/sync/engine.go`:
```go
type Engine struct {
	// …existing…
	downloaders map[int64]*AttachmentDownloader
	attachDir   string
}

func NewEngineWithDir(s *storage.Store, sec *secrets.Store, em *api.Emitter, attachDir string) *Engine {
	e := NewEngine(s, sec, em)
	e.attachDir = attachDir; e.downloaders = map[int64]*AttachmentDownloader{}
	return e
}

func (e *Engine) StartAccount(parent context.Context, id int64) {
	// existing logic…
	if e.attachDir != "" {
		d := NewAttachmentDownloader(id, e.store, e.secrets, e.em, e.attachDir)
		e.downloaders[id] = d
		go d.Run(parent)
	}
}
```

(Update `StopAccount` similarly — for now downloaders are tied to the parent ctx; precise cancel-per-account is fine to add later.)

- [ ] **Step 2: main.go**

In both `runBrowser` and `runDesktop`:
```go
eng := sync.NewEngineWithDir(st, sec, em, paths.AttachmentsDir)
go eng.Run(ctx)
```

- [ ] **Step 3: Commit**

```bash
git add internal/sync/engine.go cmd/spk-mail/
git commit -m "feat(sync): Engine starts AttachmentDownloader per account"
```

---

## Task 5: API for opening attachments

**Files:** modify `internal/api/api.go`, `internal/api/stub.go`, `internal/api/transport/{http,wails}.go`

- [ ] **Step 1: Test**

`internal/api/attachments_test.go`:
```go
package api

import (
	"context"
	"path/filepath"
	"testing"
	"github.com/spk/spk-mail/internal/api/testapi"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestGetAttachmentLocalPath_NotDownloaded(t *testing.T) {
	a := testapi.NewStub(t).(*Stub)
	ctx := context.Background()
	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{Name:"X",Email:"a@x",IMAPHost:"h",IMAPPort:993,IMAPUsername:"u",UseTLS:true,Color:"#fff",CreatedAt:0})
	fID, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter:"/", UIDValidity:1, UIDNext:1})
	mID, _ := a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fID, UID:1, Date:0, Flags:"[]"})
	aID, _ := a.Store.InsertAttachment(ctx, storage.AttachmentRow{MessageID: mID, PartID:"1", Filename:"x.bin", ContentType:"application/octet-stream", SizeBytes:4})

	_, err := a.GetAttachmentLocalPath(ctx, aID)
	require.ErrorIs(t, err, ErrAttachmentNotReady)
	_ = filepath.Separator
}
```

- [ ] **Step 2: Implement**

In `internal/api/api.go`:
```go
type API interface {
	// …existing…
	GetAttachmentLocalPath(ctx context.Context, id int64) (string, error)
	OpenAttachment(ctx context.Context, id int64) error
}
```

In `internal/api/stub.go`:
```go
import (
	"errors"
	"os/exec"
)

var ErrAttachmentNotReady = errors.New("attachment not yet downloaded")

func (s *Stub) GetAttachmentLocalPath(ctx context.Context, id int64) (string, error) {
	row := s.Store.DB().QueryRowContext(ctx, `SELECT local_path FROM attachments WHERE id = ?`, id)
	var lp *string
	if err := row.Scan(&lp); err != nil { return "", err }
	if lp == nil || *lp == "" { return "", ErrAttachmentNotReady }
	if _, err := os.Stat(*lp); err != nil {
		// File missing — clear so the downloader will re-fetch
		_ = s.Store.ClearAttachmentLocalPath(ctx, id)
		return "", ErrAttachmentNotReady
	}
	return *lp, nil
}

func (s *Stub) OpenAttachment(ctx context.Context, id int64) error {
	path, err := s.GetAttachmentLocalPath(ctx, id)
	if err != nil { return err }
	cmd := exec.CommandContext(ctx, "xdg-open", path)
	// Detach: don't wait, don't capture stdout/stderr
	return cmd.Start()
}
```

(Add `import "os"` if missing.)

- [ ] **Step 3: HTTP route + Wails method**

`http.go`:
```go
h.mux.HandleFunc("POST /api/GetAttachmentLocalPath", h.handle(func(ctx context.Context, req *struct{ ID int64 `json:"id"` }) (any, error) { return h.api.GetAttachmentLocalPath(ctx, req.ID) }))
h.mux.HandleFunc("POST /api/OpenAttachment",         h.handle(func(ctx context.Context, req *struct{ ID int64 `json:"id"` }) (any, error) { return nil, h.api.OpenAttachment(ctx, req.ID) }))
```

`wails.go`:
```go
func (w *Wails) GetAttachmentLocalPath(id int64) (string, error) { return w.a.GetAttachmentLocalPath(context.Background(), id) }
func (w *Wails) OpenAttachment(id int64) error                   { return w.a.OpenAttachment(context.Background(), id) }
```

- [ ] **Step 4: Commit**

```bash
go test ./internal/api/...
git add internal/api/
git commit -m "feat(api): GetAttachmentLocalPath + OpenAttachment via xdg-open"
```

---

## Task 6: Frontend — clickable attachment chips

**Files:** `frontend/src/components/AttachmentChip.tsx`, modify `MessageBubble.tsx`, `api/client.ts`, `api/types.ts`

- [ ] **Step 1: client**

`frontend/src/api/client.ts` — add to `Client`:
```ts
openAttachment(id: number): Promise<void>
```

HTTP impl:
```ts
openAttachment: (id) => post('/api/OpenAttachment', { id }),
```

Wails impl:
```ts
openAttachment: (id) => window.wails!.CallByName('api.OpenAttachment', id).then(() => undefined),
```

- [ ] **Step 2: AttachmentChip**

`frontend/src/components/AttachmentChip.tsx`:
```tsx
import { useState } from 'react'
import { client } from '../api/client'
import type { AttachmentDTO } from '../api/types'

export default function AttachmentChip({ a }: { a: AttachmentDTO }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string>()

  const open = async () => {
    setBusy(true); setErr(undefined)
    try { await client.openAttachment(a.id) }
    catch (e) { setErr(String(e)) }
    finally { setBusy(false) }
  }

  return (
    <button
      type="button" onClick={open} disabled={!a.downloaded || busy}
      title={a.downloaded ? 'Open with system handler' : 'Downloading…'}
      className={`text-xs rounded border px-2 py-1 inline-flex items-center gap-1 ${
        a.downloaded ? 'border-zinc-700 hover:bg-zinc-800' : 'border-zinc-800 text-zinc-500 cursor-progress'
      }`}>
      <span>📎</span>
      <span className="truncate max-w-[12rem]">{a.filename}</span>
      <span className="text-zinc-500">({Math.round(a.size_bytes/1024)} KB)</span>
      {!a.downloaded && <span className="ml-1 size-1.5 rounded-full bg-amber-400 animate-pulse" />}
      {err && <span className="text-red-400 ml-1">!</span>}
    </button>
  )
}
```

- [ ] **Step 3: Update MessageBubble**

In `frontend/src/components/MessageBubble.tsx`, replace the inline `<span>` chips with `<AttachmentChip a={a} />`.

- [ ] **Step 4: Subscribe to AttachmentReady event**

In `frontend/src/api/events.ts`, add a case:
```ts
case 'AttachmentReady':
  if (s.openThreadId !== undefined) {
    s.setOpenThread(s.openThreadId, await client.getThread(s.openThreadId))
  }
  break
```

And add `'AttachmentReady'` to the `EventType` union and to the SSE event-name list in `client.ts`.

- [ ] **Step 5: Build + commit**

```bash
cd frontend && npm run build && cd .. && make build
git add frontend/
git commit -m "feat(frontend): clickable attachment chips with download status and re-fetch"
```

---

## Task 7: Playwright test for attachments

**Files:** `tests/playwright/attachments.spec.ts`, expand `tests/fixtures/basic.yaml`

- [ ] **Step 1: Update fixture (optional)**

If `tests/fixtures/basic.yaml` doesn't already have an attachment, ensure at least one message in INBOX has one (the seed handler already creates the attachment row; the downloader writes the file).

- [ ] **Step 2: Test**

`tests/playwright/attachments.spec.ts`:
```ts
import { test, expect } from '@playwright/test'

test('attachment chip becomes enabled after download', async ({ page }) => {
  await page.goto('/')
  await page.getByText(/Weekly digest/).click()
  // chip starts disabled (downloading); poll for enable
  const chip = page.getByRole('button', { name: /\.pdf/ }).first()
  await expect(chip).toBeVisible({ timeout: 10_000 })
  await expect(chip).toBeEnabled({ timeout: 15_000 })
})
```

- [ ] **Step 3: Run + commit**

```bash
cd tests/playwright && npx playwright test attachments.spec.ts && cd ../..
git add tests/playwright/
git commit -m "test(ui): playwright proves attachment chip enables after download"
```

---

## Self-Review

**Spec coverage added by plan 6:**
- §8.4 attachment download flow — Tasks 2, 3, 4.
- Eager downloads, low-priority connection that does not block IDLE — Task 3 (separate connection per `runOnce`).
- Local-file missing → re-download — Task 5 (`GetAttachmentLocalPath` clears `local_path` and downloader will pick it up next tick).
- Open with system handler (`xdg-open`) — Task 5.

**Gaps:** none for the spec. The downloader does not yet retry permanent errors with backoff; any error just gets retried at the next 5s tick. That's fine in practice; if needed later, add an `error_count`/`next_attempt_at` column.

**Type consistency:**
- `AttachmentDTO.downloaded` field already existed in plan 1 — used here without modification ✓.
- New event `AttachmentReady` is added consistently across emitter (Go), SSE event list (TS), Wails event list (TS), and event-handling switch (`useEvents`) ✓.
