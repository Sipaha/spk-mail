# spk-mail — Design Spec

**Status:** draft, awaiting user approval
**Date:** 2026-04-27
**Reference (UX/feature inspiration):** Mailspring
**Reference (project layout / tech stack):** `~/IdeaProjects/citeck-launcher2`

## 1. Goal

A desktop email client for Linux that lives in the system tray and shows native notifications when new mail arrives. Single Go binary with an embedded React webview (Wails v3). Receives mail over IMAP only — no sending in this version.

## 2. Scope

In scope:

- Multiple IMAP accounts with a unified inbox view (per-account color tags) plus per-account/per-folder filtering.
- All folders synced (INBOX, Sent, custom — whatever the server lists).
- IMAP IDLE for real-time push, with automatic fallback to polling for servers without IDLE.
- App Password auth only (no OAuth flows). Passwords stored encrypted on disk.
- Client-side conversation threading via RFC 5322 `Message-ID` / `In-Reply-To` / `References` headers (jwz-thread style).
- HTML email rendering with sanitization and remote-content blocking by default.
- Eager attachment download (downloaded immediately after the message body, in the background).
- Full-text search via SQLite FTS5 with simple operators (`from:`, `subject:`, `has:attachment`, free-text).
- System tray with unread badge; closing the window minimizes to tray. Quit only via tray menu.
- Native desktop notifications on new INBOX messages.
- A `--browser` mode that exposes the same UI as a localhost HTTP app, so Claude Code (via Playwright MCP) can navigate, screenshot, and exercise the UI deterministically against an in-process mock IMAP server with seeded fixtures.

Out of scope (future):

- Sending mail (SMTP, compose, drafts, signatures).
- OAuth providers (Gmail/Outlook OAuth flow, refresh tokens).
- Server-side IMAP THREAD/REFERENCES extension (we always do client-side threading).
- Windows / macOS support (architecture stays portable; build matrix is added later).
- Plugins, themes beyond a default dark theme, snooze, calendar integration, contacts.
- Per-message snooze / reminders / focused inbox.
- Mobile.

## 3. Tech stack

| Layer | Choice | Rationale |
|---|---|---|
| Backend language | Go 1.26 | Match `citeck-launcher2`. |
| Desktop runtime | Wails v3 (alpha) | Native window + tray + notifications; same as launcher. |
| Frontend | React 19 + Vite + TS + Tailwind 4 | Same as launcher. |
| State (frontend) | Zustand | Lightweight, no Redux ceremony. |
| DB | SQLite via `modernc.org/sqlite` | Pure-Go (no extra CGO surface), FTS5 included. |
| IMAP client | `github.com/emersion/go-imap` v2 | Active, supports IDLE, has `imapmemserver` for tests. |
| MIME parsing | `github.com/emersion/go-message` | Same author, integrates with go-imap v2. |
| HTML sanitizer | `github.com/microcosm-cc/bluemonday` | De-facto standard for Go. |
| Secrets at rest | AES-256-GCM, master key in OS keyring | `github.com/zalando/go-keyring`; PBKDF2-SHA256 fallback prompt if keyring missing. |
| Logging | `log/slog` + rotating file writer | Borrow `RotatingWriter` pattern from launcher. |
| CLI flags | `github.com/spf13/cobra` | Match launcher. |
| Build | Make + Taskfile | Match launcher. |

## 4. Platforms

Linux only for the first release. Wails v3 itself is portable; build matrix for Windows/macOS is left for a follow-up.

Linux tray quirks (must be documented in the README):

- GNOME: requires the AppIndicator extension (`gnome-shell-extension-appindicator`).
- KDE Plasma / XFCE / Cinnamon / MATE: works out of the box.
- Wayland-only sessions: tray support depends on the compositor (Sway has issues; KDE Wayland is fine).

Native notifications use `org.freedesktop.Notifications` (D-Bus), which works across all major desktop environments.

## 5. Architecture

### 5.1 Process model

Single Wails v3 process. Closing the window minimizes to tray; the process keeps running, IDLE connections stay open, sync continues. Quit only from tray menu (`Quit spk-mail`).

```
┌──────────────────── spk-mail (Wails) ────────────────────┐
│                                                          │
│  ┌─ React webview (frontend/) ─┐    ┌─ Tray icon ─┐      │
│  │   Inbox / Thread / Settings │    │ unread cnt  │      │
│  └──────────────┬──────────────┘    └──────┬──────┘      │
│                 │ Wails bind                │             │
│  ┌──────────────▼────────────────────┐  ┌───▼──────────┐ │
│  │   API layer (internal/api)        │  │ tray ctrl    │ │
│  │   commands + events               │  │ (notify+menu)│ │
│  └──────────────┬────────────────────┘  └──────┬───────┘ │
│                 │                              │         │
│  ┌──────────────▼──────────────────────────────▼───────┐ │
│  │  Sync engine (internal/sync)                        │ │
│  │  AccountWorker × N ──► StoreWriter ──► SQLite       │ │
│  │  (IDLE + poll)         (single writer)              │ │
│  └──────────────────────┬──────────────────────────────┘ │
│                         │                                │
│  ┌──────────────────────▼──────────────┐                 │
│  │  Storage (internal/storage)         │                 │
│  │  SQLite (messages/threads/FTS5)     │                 │
│  │  + filesystem (attachments)         │                 │
│  └─────────────────────────────────────┘                 │
└──────────────────────────────────────────────────────────┘
```

### 5.2 Sync engine

**Model: per-account worker + central writer.**

- One `AccountWorker` goroutine per account.
- Each worker holds its own IMAP connection(s); for IDLE it opens additional connections (one per actively-IDLE'd folder) up to a per-server cap of 5.
- All workers send parsed messages to a single `StoreWriter` goroutine via a buffered channel. The `StoreWriter` is the only thing that writes to SQLite — eliminates writer contention, batches inserts in transactions.
- Reads from SQLite happen directly from the API layer (SQLite WAL allows concurrent readers).

```go
type Engine struct {
    workers    map[int64]*AccountWorker  // account_id -> worker
    writer     *StoreWriter
    incoming   chan IncomingMessage      // parsed messages
    flagOps    chan FlagOp               // outbound flag changes
    events     chan api.Event
    superv     *Supervisor               // restarts crashed workers w/ backoff
}
```

`AccountWorker` lifecycle:

1. Connect, login (over implicit TLS or STARTTLS).
2. `LIST "*"` → upsert `folders` rows.
3. For each folder: `SELECT`, then `UID FETCH 1:* (UID FLAGS ENVELOPE BODYSTRUCTURE RFC822.SIZE BODY[TEXT])` for headers/text. HTML parts are fetched the same way. Stream messages into the `incoming` channel.
4. After full sync of a folder, save `UIDVALIDITY` and current `UIDNEXT`.
5. Start an `IDLE` goroutine per folder (up to the per-server cap; remaining folders fall back to per-folder polling every 60s).
6. Subscribe to `flagOps` channel for outgoing flag changes (e.g. mark read) and translate them to IMAP `STORE` commands.

Supervision: if a worker goroutine returns a non-recoverable error or panics, the supervisor restarts it with exponential backoff (1s, 2s, 5s, 15s, 60s, 300s capped). Network/IMAP errors *inside* the worker are handled internally with their own retry logic and do not bubble up to the supervisor.

### 5.3 Storage layout (XDG)

| Path | Content |
|---|---|
| `~/.config/spk-mail/config.yml` | Accounts (host/port/user/email/color), UI prefs |
| `~/.local/share/spk-mail/db.sqlite` | All messages, threads, attachments metadata, FTS5 index |
| `~/.local/share/spk-mail/db.sqlite-wal` / `-shm` | SQLite WAL files |
| `~/.local/share/spk-mail/attachments/<account_id>/<message_id>/<filename>` | Downloaded attachment files |
| `~/.local/share/spk-mail/secrets.bin` | AES-256-GCM blob: per-account IMAP passwords |
| `~/.local/share/spk-mail/logs/spk-mail.log` | Rotating slog output |

Master key: random 32 bytes generated on first run, stored in OS keyring under service `spk-mail`, account `master-key`. If the keyring is unavailable at startup, prompt the user for a master password; derive a 32-byte key with PBKDF2-HMAC-SHA256 (1M iterations, 16-byte salt stored alongside `secrets.bin`).

## 6. Package layout

```
spk-mail/
├── cmd/
│   └── spk-mail/main.go         # Wails app bootstrap, CLI flags
├── internal/
│   ├── api/
│   │   ├── api.go               # API interface (transport-agnostic)
│   │   ├── commands.go          # method implementations
│   │   ├── events.go            # event types + emitter
│   │   ├── dto.go               # MessageDTO, ThreadDTO, AccountDTO, …
│   │   └── transport/
│   │       ├── wails.go         # binds API to Wails service
│   │       └── http.go          # binds API to /api/* + SSE /api/events
│   ├── config/                  # config.yml load/save, XDG paths
│   ├── secrets/                 # AES-256-GCM, keyring, PBKDF2 fallback
│   ├── storage/
│   │   ├── schema.sql           # embedded migrations
│   │   ├── store.go             # open, migrate, common helpers
│   │   ├── messages.go
│   │   ├── threads.go
│   │   ├── search.go            # FTS5 query parser + executor
│   │   └── attachments.go
│   ├── imap/
│   │   ├── client.go            # connect, auth, capabilities, retry policy
│   │   ├── idle.go              # IDLE loop (28-min refresh, error handling)
│   │   └── fetch.go             # FETCH wrappers
│   ├── mime/
│   │   ├── parser.go            # raw → headers/body/parts/attachments
│   │   └── sanitize.go          # bluemonday HTML sanitizer + remote-content rewriter
│   ├── thread/
│   │   └── jwz.go               # client-side threading
│   ├── sync/
│   │   ├── engine.go            # orchestrator + supervisor
│   │   ├── account_worker.go    # per-account goroutine
│   │   ├── folder_state.go      # UIDVALIDITY/UIDNEXT tracking
│   │   ├── store_writer.go      # singleton DB writer
│   │   └── attachments.go       # background attachment download worker
│   ├── tray/
│   │   ├── tray.go              # menu, icon, badge
│   │   └── notify.go            # native notifications
│   ├── testapi/                 # test-mode HTTP routes (browser mode only)
│   │   ├── seed.go              # POST /api/_test/seed
│   │   ├── inject.go            # POST /api/_test/inject-message
│   │   ├── clock.go             # POST /api/_test/clock
│   │   ├── dbdump.go            # GET  /api/_test/db-dump
│   │   └── logs.go              # GET  /api/_test/logs
│   ├── mockimap/                # in-process IMAP server (uses imapmemserver)
│   ├── fsutil/                  # atomic write, hashing, attachments dir
│   └── appfiles/                # //go:embed icons, default config, fixtures
└── frontend/                    # React 19 + Vite + TS + Tailwind 4
    ├── src/
    │   ├── pages/               # Inbox, Thread, Settings (Accounts, Appearance)
    │   ├── components/          # MessageList, ThreadView, AccountSidebar, …
    │   ├── api/
    │   │   └── client.ts        # detects window.wails vs HTTP, exposes the same surface
    │   ├── store/               # Zustand
    │   └── styles/              # tailwind.css
    ├── index.html
    ├── package.json
    └── vite.config.ts
```

### Boundary rules

- `internal/imap/` is the only package that knows the IMAP wire protocol. It returns plain Go structs.
- `internal/storage/` is the only package that issues SQL. Everything else uses its methods.
- `internal/sync/` orchestrates lifecycle and concurrency. It does not see SQL or IMAP bytes — only domain types.
- `internal/api/` is the only place the frontend talks to. Both transports (Wails, HTTP) call into the same API methods.
- `internal/tray/` is UI-effect-only. It subscribes to events from `sync/` and renders state.
- `internal/testapi/` is wired only when the `--browser` flag is set, never in desktop builds.

## 7. Database schema

SQLite, `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`. Migrations embedded via `//go:embed schema.sql`.

```sql
CREATE TABLE accounts (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    email           TEXT NOT NULL UNIQUE,
    imap_host       TEXT NOT NULL,
    imap_port       INTEGER NOT NULL,
    imap_username   TEXT NOT NULL,
    use_tls         INTEGER NOT NULL,    -- 0/1
    color           TEXT NOT NULL,       -- hex; used as account tag in unified inbox
    created_at      INTEGER NOT NULL
);

CREATE TABLE folders (
    id              INTEGER PRIMARY KEY,
    account_id      INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,       -- IMAP path, e.g. "INBOX/Work"
    delimiter       TEXT NOT NULL,
    role            TEXT,                -- inbox|sent|drafts|trash|spam|archive|null
    uid_validity    INTEGER NOT NULL,
    uid_next        INTEGER NOT NULL,
    last_synced_at  INTEGER,
    UNIQUE(account_id, name)
);

CREATE TABLE messages (
    id              INTEGER PRIMARY KEY,
    account_id      INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    folder_id       INTEGER NOT NULL REFERENCES folders(id) ON DELETE CASCADE,
    uid             INTEGER NOT NULL,        -- IMAP UID inside folder
    message_id      TEXT,                    -- RFC 5322 Message-ID
    in_reply_to     TEXT,
    references_     TEXT,                    -- space-joined References header
    thread_id       INTEGER REFERENCES threads(id) ON DELETE SET NULL,
    subject         TEXT,
    from_addr       TEXT,                    -- "Name <email>"
    to_addrs        TEXT,                    -- JSON array of strings
    cc_addrs        TEXT,
    date            INTEGER NOT NULL,        -- epoch seconds
    flags           TEXT NOT NULL,           -- JSON array, e.g. ["\\Seen","\\Flagged"]
    has_attachments INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    body_text       TEXT,
    body_html       TEXT,                    -- already sanitized at insert time
    UNIQUE(account_id, folder_id, uid)
);
CREATE INDEX idx_messages_thread ON messages(thread_id);
CREATE INDEX idx_messages_date   ON messages(date DESC);
CREATE INDEX idx_messages_msgid  ON messages(message_id);

CREATE TABLE threads (
    id              INTEGER PRIMARY KEY,
    subject_norm    TEXT NOT NULL,           -- subject without Re:/Fwd: prefixes
    last_date       INTEGER NOT NULL,
    msg_count       INTEGER NOT NULL DEFAULT 0,
    unread_count    INTEGER NOT NULL DEFAULT 0,
    has_flagged     INTEGER NOT NULL DEFAULT 0,
    has_attach      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_threads_last_date ON threads(last_date DESC);

CREATE TABLE attachments (
    id              INTEGER PRIMARY KEY,
    message_id      INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    part_id         TEXT NOT NULL,           -- IMAP BODYSTRUCTURE part path "1.2"
    filename        TEXT NOT NULL,
    content_type    TEXT NOT NULL,
    size_bytes      INTEGER NOT NULL,
    sha256          TEXT,
    local_path      TEXT,                    -- abs path; NULL = not yet downloaded
    downloaded_at   INTEGER
);
CREATE INDEX idx_attachments_msg ON attachments(message_id);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    subject, from_addr, to_addrs, body_text,
    content='messages', content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);

CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, subject, from_addr, to_addrs, body_text)
    VALUES (new.id, new.subject, new.from_addr, new.to_addrs, new.body_text);
END;
CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_addr, to_addrs, body_text)
    VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addrs, old.body_text);
END;
CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, subject, from_addr, to_addrs, body_text)
    VALUES ('delete', old.id, old.subject, old.from_addr, old.to_addrs, old.body_text);
    INSERT INTO messages_fts(rowid, subject, from_addr, to_addrs, body_text)
    VALUES (new.id, new.subject, new.from_addr, new.to_addrs, new.body_text);
END;

CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);
```

Notes:

- A single RFC message that exists in multiple folders becomes multiple `messages` rows (one per folder). Conversation threading reunites them via `message_id`.
- `body_html` is stored already-sanitized; the frontend simply puts it into a sandboxed iframe. This avoids drift between two sanitizers.
- `flags` is JSON, not a bitmask — IMAP allows custom keywords, and JSON is forward-compatible.
- Thread resolution at insert time: look up existing messages by `message_id` ↔ `in_reply_to`/`references`, reuse their `thread_id` or create a new thread.
- `UIDVALIDITY` change for a folder ⇒ truncate `messages WHERE folder_id = ?`, full re-sync. Notifications are suppressed during re-sync (a `is_resync` flag on the worker).

## 8. Data flows

### 8.1 Add account / first sync

```
User submits Settings → Add Account form
  → api.AddAccount(name, email, host, port, user, pass, useTLS, color)
  → secrets.Store("account:<id>", pass)
  → storage.InsertAccount(...)
  → sync.Engine.StartAccount(id)

AccountWorker(id) goroutine:
  1. imap.Client.Connect() + LOGIN  (or AUTHENTICATE)
  2. LIST "*"  →  upsert folders rows (UIDVALIDITY, UIDNEXT=0)
  3. for each folder:
       SELECT folder
       UID FETCH 1:* (UID FLAGS ENVELOPE BODYSTRUCTURE RFC822.SIZE BODY[TEXT/HTML])
       stream → incomingMessages channel
       StoreWriter:
         - sanitize HTML (bluemonday + remote-content rewrite)
         - parse MIME (envelope, attachments metadata, part_ids)
         - INSERT message
         - resolve thread_id, INSERT/UPDATE threads
         - emit api.Event "MessageInserted"
  4. UPDATE folders SET uid_next=<server UIDNEXT>, last_synced_at=now
  5. start IDLE goroutine per folder (up to cap; rest get per-folder poll)
  6. enqueue attachment downloads in AttachmentDownloader
```

Sync progress is streamed to the frontend as `SyncProgress { account_id, folder_id, done, total }` events; the sidebar shows a spinner per account during initial sync.

### 8.2 IDLE / new message

```
IDLE goroutine for (account, folder):
  loop:
    SELECT folder
    IDLE
    on EXISTS / RECENT / FETCH (server pushes notification):
      DONE
      UID FETCH <uid_next>:* (...)
      stream → StoreWriter
      after INSERT:
        emit "MessageArrived" event { message, account, folder }
        if folder.role == "inbox" and !\Seen:
          tray.NotifyNewMessage(...)
    on timeout (28 min):
      DONE → reissue IDLE
    on connection error:
      backoff (1s, 2s, 5s, 10s, 30s, 60s capped)
      reconnect
    on missing IDLE capability:
      switch to poll mode for this folder (UID SEARCH UID <uid_next>:*  every 60s)
```

### 8.3 Open thread

```
User clicks a thread in the unified inbox
  → api.GetThread(thread_id)
  → SELECT messages WHERE thread_id = ? ORDER BY date
  → return [MessageDTO] with body_html (pre-sanitized) + attachment metadata

Frontend:
  for each message → render in <iframe sandbox="allow-same-origin"> (no allow-scripts)
  remote images already rewritten src → data-src + placeholder
  user clicks "Show remote content":
    → api.AllowRemoteForMessage(id)
    → Go restores src and returns updated HTML

Auto-mark-read:
  on thread open → api.MarkRead(message_ids)
  → UPDATE messages SET flags
  → AccountWorker.QueueFlagOp(STORE +FLAGS \Seen, uid)
  → emit "MessageUpdated"
```

### 8.4 Attachment download (eager)

When `StoreWriter` inserts a message, attachments rows are created with `local_path = NULL`. A separate per-account `AttachmentDownloader` worker consumes them in `messages.date DESC` order:

1. UID FETCH BODY[part_id] (uses a dedicated low-priority IMAP connection so it does not block IDLE).
2. Atomic write to `attachments/<account_id>/<message_id>/<filename>`.
3. Compute SHA-256.
4. UPDATE attachments SET local_path, sha256, downloaded_at.
5. Emit `AttachmentReady { message_id, attachment_id }`.

If a file is missing on disk (user deleted it), the next request to open it triggers a re-download.

### 8.5 Search

```
api.Search(query: string, filters: { account_id?, folder_id?, has_attach?, unread? })

Internal parser:
  "from:bob unread invoice"  →
    FTS-MATCH "invoice" AND from_addr LIKE '%bob%' AND flags NOT LIKE '%\\Seen%'

SELECT m.*, snippet(messages_fts, 3, '<mark>', '</mark>', '…', 16) AS snippet
FROM messages_fts
JOIN messages m ON m.id = messages_fts.rowid
WHERE messages_fts MATCH ?
  AND <filters>
ORDER BY m.date DESC
LIMIT 100 OFFSET ?

Results: flat list of messages (not threads). Click → opens parent thread with the message scrolled into view and the matching snippet highlighted.
```

## 9. Error handling

Principles:

1. Network / IMAP errors never crash the process. They are isolated inside `AccountWorker` and translate into backoff + reconnect.
2. MIME parse errors mark the message with a placeholder (`subject="(broken message)"`, body_text=error) so the UID is not reprocessed forever.
3. SQLite write errors are fatal (disk / corruption). The app shows a dialog and exits.
4. All errors go through `slog` to the rotating log file.

| Situation | Behavior |
|---|---|
| IMAP login fails (auth) | Account status `error: auth_failed`, red sidebar icon, frontend toast. No automatic retry until settings are edited. |
| TLS handshake fails | If `use_tls=true` try STARTTLS first; otherwise `error: tls_failed`. |
| Network unavailable | Backoff up to 60s, transparent to UI (small offline indicator in tray + sidebar). Auto-resume on recovery. |
| Server lacks IDLE | Folder switches to poll mode; `INFO` log; sidebar shows "poll" badge in account info. |
| `UIDVALIDITY` change | Truncate `messages WHERE folder_id=?`, full re-fetch. Notifications suppressed during re-sync. |
| Server closed the connection | Reconnect, re-SELECT, re-IDLE. Transparent. |
| Attachment file missing locally | Lazy re-download on access; row resets to `local_path=NULL`. |
| Keyring unavailable at startup | Master-password prompt; PBKDF2-SHA256 (1M iterations, 16-byte salt). Key kept in memory only. |
| SQLite migration fails | Backup current `db.sqlite` to `db.sqlite.bak.<timestamp>`, dialog + exit. |

Supervision: `Engine` restarts crashed `AccountWorker`s with exponential backoff (1s, 2s, 5s, 15s, 60s, 300s capped). This is distinct from in-worker network retries.

## 10. Testing

| Level | What it covers | Tools |
|---|---|---|
| Go unit | MIME parser, sanitizer, jwz-thread, search-query parser, schema migrations, AES round-trip, FTS5 SQL | `testing` + `testify` |
| Storage integration | Full insert→select cycle on real SQLite (`:memory:` or `t.TempDir()`), including FTS5 triggers and thread upsert logic | `testing` |
| IMAP integration | `AccountWorker` against `imapmemserver`: new message, flag change, move, UIDVALIDITY change, IDLE timeout, attachment fetch | `imapmemserver` |
| Sync engine | `Engine` with two mock accounts against `imapmemserver`, concurrent inserts via single `StoreWriter`, supervision | `imapmemserver` + race detector |
| Frontend unit | Components with conditional rendering (`MessageList`, `ThreadView`, search dropdown) | Vitest + RTL |
| E2E | Built binary in `--browser` mode + mock IMAP + seed fixture, driven through Playwright MCP | Playwright |

CI targets: `make test` runs Go (`-race -timeout 120s`) + Vitest. `make lint` runs `golangci-lint` and `eslint`.

Coverage targets: `internal/storage`, `internal/thread`, `internal/mime`, `internal/sync` ≥ 80%. `internal/imap`, `internal/tray` get smoke + integration only.

Manual verification before merging UI features: per the global CLAUDE.md rule for UI changes, any UI feature is exercised end-to-end (add account → wait for sync → minimize → trigger arrival → see notification + tray badge → open from tray → read → confirm `\Seen` lands on the server). For spk-mail this manual walk is performed in the webview (or in `--browser` mode via Playwright MCP).

## 11. Browser mode + UI automation API

The same binary supports two run modes:

| Mode | Invocation | Has | Lacks |
|---|---|---|---|
| Desktop (default) | `spk-mail` | Wails webview, tray, notifications, IMAP sync, DB | — |
| Browser | `spk-mail --browser --port=5174` | HTTP server serving the embedded React UI, full Go API over REST + SSE, IMAP sync, DB | Tray, native notifications |

Implementation: `internal/api/` defines a transport-agnostic `API` interface. `transport/wails.go` binds it as Wails services (production desktop). `transport/http.go` exposes the same methods under `POST /api/<method>` with JSON bodies, plus an SSE stream at `/api/events` carrying the same events that the Wails transport emits. The frontend (`frontend/src/api/client.ts`) detects the runtime: if `window.wails` exists it uses Wails bindings, otherwise it `fetch`es `/api/*` and listens to `EventSource('/api/events')`. One frontend codebase, two transports.

### 11.1 In-process IMAP mock

Flag `--imap-mock` starts an in-memory IMAP server (`go-imap` v2's `imapserver` + `imapmemserver`) on a localhost port chosen at startup (logged to stdout). Any account whose `use_mock: true` field is set is rewritten at config-load time to point at this local server. This allows fully deterministic UI runs with no network.

### 11.2 Seed fixtures

Flag `--seed=<path>` loads a YAML fixture into the mock server and into `accounts`/`folders`/`messages`/`threads`/`attachments` tables before the UI starts. Example:

```yaml
# tests/fixtures/basic.yaml
accounts:
  - name: "Test Personal"
    email: "alice@example.com"
    color: "#3b82f6"
    use_mock: true
    folders:
      - name: INBOX
        messages:
          - from: "Bob <bob@example.com>"
            subject: "Project update"
            date: 2026-04-27T10:30:00Z
            body_text: "Hi Alice, here's the latest…"
            flags: []
          - from: "Newsletter <news@example.com>"
            subject: "Weekly digest #42"
            date: 2026-04-26T08:00:00Z
            body_html: "<h1>Top stories</h1>…"
            attachments:
              - filename: report.pdf
                content_type: application/pdf
                size: 102400
```

### 11.3 Test-only HTTP routes

Mounted only when `--browser` is set, under `/api/_test/*`:

| Route | Purpose |
|---|---|
| `POST /api/_test/seed` | Load a fixture (YAML or JSON body) into the mock server + DB at runtime |
| `POST /api/_test/inject-message` | Synthesize a message arriving at the mock server, exercising the IDLE → notification flow |
| `POST /api/_test/clock` | Freeze "now" for deterministic relative timestamps in screenshots |
| `GET  /api/_test/db-dump` | JSON snapshot of all tables for assertions |
| `GET  /api/_test/logs` | Recent slog entries (in-memory ring buffer) |

These are registered only inside `if cfg.BrowserMode { router.Mount("/api/_test", testRoutes) }` so the desktop binary does not expose them.

### 11.4 Driving the UI from Claude Code

The frontend is reachable at `http://localhost:5174` and Playwright MCP (`plugin:playwright`) is the standard interface — `browser_navigate`, `browser_click`, `browser_type`, `browser_take_screenshot`, `browser_snapshot`, `browser_console_messages`, `browser_network_requests`. No spk-mail-specific automation client is needed.

A short `docs/ui-testing.md` guide (written as part of the implementation) documents:

- How to launch with a fixture (`spk-mail --browser --imap-mock --seed=tests/fixtures/basic.yaml --port=5174`).
- Recipe: verify the new-message notification flow (POST `/api/_test/inject-message` then assert tray-badge SSE event and message visibility in the inbox).
- Recipe: visual regression run (set `--browser`, seed, freeze clock, take screenshots of each page).
- How to add a new fixture file.

This page is referenced from `CLAUDE.md` so future sessions know the entry point.

## 12. Open questions / deferred decisions

- Default dark theme styling: details deferred to implementation, modeled on launcher's Darcula/Lens theme.
- Choice between auto-start on login and manual: default off, exposed in Settings.
- Specific Linux distros / DEs to ship pre-built bundles for: deferred to release time.
- Whether to ship a `.deb` and AppImage in addition to the raw binary: deferred.
