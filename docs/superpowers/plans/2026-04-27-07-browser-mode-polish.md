# Browser-Mode Polish + UI Testing Docs (Plan 7 of 7)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisites:** Plans 1–6 merged.

**Goal:** Finalize the `--browser` mode automation surface so Claude Code (via Playwright MCP) can drive every interesting flow deterministically. Add the deferred test routes (`inject-message` is already in plan 2; this plan adds `clock`, expands `db-dump`, and wires the slog→ring-buffer bridge for `/api/_test/logs`). Add a richer fixture, a `docs/ui-testing.md` recipe book, and a reference Playwright suite covering the full feature set. Tighten security: `_test/*` routes refuse to register unless `--browser` is set, and the desktop binary's build tag excludes them entirely.

**Architecture:** A new `clock` package provides a swappable wall-clock (default = `time.Now`); the test route swaps it to a fixed value for screenshot determinism. The slog→ring-buffer bridge is a small `slog.Handler` wrapper. `docs/ui-testing.md` is referenced from `CLAUDE.md` so future sessions find it.

**Tech Stack:** Standard library only.

---

## File Structure

Created / modified:

```
internal/clock/
├── clock.go                       # Now(), Set(), Default
└── clock_test.go

internal/testapi/
├── routes.go                      # mount real handlers for clock + replace inject stub from plan 2
├── clock.go                       # POST /api/_test/clock
├── inject.go                      # promoted from plan 2 (no change here)
├── dbdump.go                      # extended: messages + attachments + folders dumps
├── logs.go                        # extended: real slog.Handler + ring-buffer bridge
└── routes_test.go                 # updated tests

cmd/spk-mail/main.go                # build_tag-gated mounting; slog handler wired

tests/fixtures/
├── basic.yaml                      # already exists (plan 1)
├── multi-account.yaml              # NEW: 2 accounts, threading across them
├── attachments.yaml                # NEW: messages with PDFs/images
└── html-tracking.yaml              # NEW: HTML email with remote tracking pixel

tests/playwright/
├── add-account.spec.ts             # NEW: form submission flow
├── notification-flow.spec.ts       # NEW: inject + assert SSE event seen + thread updated
├── unblock-remote.spec.ts          # NEW: HTML body with blocked image
├── threading.spec.ts               # NEW: reply threading
└── visual-regression.spec.ts       # NEW: takes screenshots of every page (clock-frozen)

docs/
├── ui-testing.md                   # NEW: recipe book for Claude
└── superpowers/specs/2026-04-27-spk-mail-design.md  (already committed)

CLAUDE.md                            # NEW project-level CLAUDE.md referencing docs/ui-testing.md
```

---

## Task 1: clock package

**Files:** `internal/clock/clock.go`, `internal/clock/clock_test.go`

- [ ] **Step 1: Tests**

`internal/clock/clock_test.go`:
```go
package clock

import (
	"testing"
	"time"
	"github.com/stretchr/testify/require"
)

func TestNow_DefaultsToWallClock(t *testing.T) {
	c := New()
	got := c.Now()
	require.WithinDuration(t, time.Now(), got, time.Second)
}

func TestNow_FixedAfterSet(t *testing.T) {
	c := New()
	fixed := time.Date(2026, 4, 27, 10, 30, 0, 0, time.UTC)
	c.Set(fixed)
	require.True(t, c.Now().Equal(fixed))
	c.Reset()
	require.WithinDuration(t, time.Now(), c.Now(), time.Second)
}
```

- [ ] **Step 2: Implement**

`internal/clock/clock.go`:
```go
// Package clock provides a swappable wall clock. By default it returns time.Now;
// browser-mode test code swaps in a fixed value via POST /api/_test/clock so
// "relative time" UI text is deterministic in screenshots.
package clock

import (
	"sync"
	"time"
)

type Clock struct {
	mu    sync.RWMutex
	fixed *time.Time
}

func New() *Clock { return &Clock{} }

func (c *Clock) Now() time.Time {
	c.mu.RLock(); defer c.mu.RUnlock()
	if c.fixed != nil { return *c.fixed }
	return time.Now()
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock(); defer c.mu.Unlock()
	c.fixed = &t
}

func (c *Clock) Reset() {
	c.mu.Lock(); defer c.mu.Unlock()
	c.fixed = nil
}

// Default is a process-wide instance used by simple callers. Production code
// that wants to be testable should accept a *Clock as a dependency rather than
// reaching into Default.
var Default = New()
```

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/clock/ -v
git add internal/clock/
git commit -m "feat(clock): swappable wall clock for deterministic test screenshots"
```

---

## Task 2: slog → ring-buffer handler

**Files:** modify `internal/testapi/logs.go`

- [ ] **Step 1: Implement**

Replace `internal/testapi/logs.go` content with:
```go
package testapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type LogEntry struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type RingBuffer struct {
	mu  sync.Mutex
	buf []LogEntry
	cap int
}

func NewRingBuffer(cap int) *RingBuffer { return &RingBuffer{cap: cap} }

func (r *RingBuffer) Append(e LogEntry) {
	r.mu.Lock(); defer r.mu.Unlock()
	if len(r.buf) >= r.cap { r.buf = r.buf[1:] }
	r.buf = append(r.buf, e)
}

func (r *RingBuffer) Snapshot() []LogEntry {
	r.mu.Lock(); defer r.mu.Unlock()
	out := make([]LogEntry, len(r.buf)); copy(out, r.buf); return out
}

func logsHandler(rb *RingBuffer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rb.Snapshot())
	}
}

// SlogHandler wraps another slog.Handler and mirrors records into a RingBuffer.
type SlogHandler struct {
	inner slog.Handler
	rb    *RingBuffer
}

func NewSlogHandler(inner slog.Handler, rb *RingBuffer) *SlogHandler {
	return &SlogHandler{inner: inner, rb: rb}
}

func (h *SlogHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.rb.Append(LogEntry{Time: time.Now().Unix(), Level: r.Level.String(), Message: r.Message})
	return h.inner.Handle(ctx, r)
}
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return &SlogHandler{inner: h.inner.WithAttrs(attrs), rb: h.rb} }
func (h *SlogHandler) WithGroup(name string) slog.Handler       { return &SlogHandler{inner: h.inner.WithGroup(name), rb: h.rb} }
```

- [ ] **Step 2: Wire in main.go**

In `cmd/spk-mail/main.go` `runBrowser`:
```go
logBuf := testapi.NewRingBuffer(500)
inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
slog.SetDefault(slog.New(testapi.NewSlogHandler(inner, logBuf)))
```

Pass `logBuf` to `testapi.Mount{Logs: logBuf}` as already done in plan 1.

- [ ] **Step 3: Test**

`internal/testapi/logs_test.go`:
```go
package testapi

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestSlogHandler_RecordsToBuffer(t *testing.T) {
	rb := NewRingBuffer(8)
	inner := slog.NewTextHandler(os.Stderr, nil)
	logger := slog.New(NewSlogHandler(inner, rb))
	logger.InfoContext(context.Background(), "hello")
	logger.WarnContext(context.Background(), "watch out")
	snap := rb.Snapshot()
	require.Len(t, snap, 2)
	require.Equal(t, "hello", snap[0].Message)
	require.Equal(t, "WARN", snap[1].Level)
}
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/testapi/ -v
git add internal/testapi/logs.go internal/testapi/logs_test.go cmd/spk-mail/
git commit -m "feat(testapi): slog→ring-buffer handler so /api/_test/logs returns recent entries"
```

---

## Task 3: clock route

**Files:** `internal/testapi/clock.go`, modify `internal/testapi/routes.go`

- [ ] **Step 1: Implement**

`internal/testapi/clock.go`:
```go
package testapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/spk/spk-mail/internal/clock"
)

type clockReq struct {
	Now   string `json:"now,omitempty"`   // RFC3339; empty + reset=true → wall clock
	Reset bool   `json:"reset,omitempty"`
}

func clockHandler(c *clock.Clock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req clockReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, err.Error(), 400); return }
		if req.Reset { c.Reset(); w.WriteHeader(204); return }
		t, err := time.Parse(time.RFC3339, req.Now)
		if err != nil { http.Error(w, "invalid 'now': "+err.Error(), 400); return }
		c.Set(t); w.WriteHeader(204)
	}
}
```

- [ ] **Step 2: Mount in routes**

`internal/testapi/routes.go`:
```go
type Mount struct {
	API   api.API
	Store *storage.Store
	Mock  *mockimap.Server
	Logs  *RingBuffer
	Clock *clock.Clock // NEW
}

func (m *Mount) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/_test/seed", &seedHandler{api: m.API, store: m.Store, mock: m.Mock})
	mux.HandleFunc("GET  /api/_test/db-dump", dbDumpHandler(m.Store))
	mux.HandleFunc("GET  /api/_test/logs",    logsHandler(m.Logs))
	mux.Handle    ("POST /api/_test/inject-message", &injectHandler{mock: m.Mock})
	mux.HandleFunc("POST /api/_test/clock",   clockHandler(m.Clock))
}
```

In `cmd/spk-mail/main.go runBrowser`, instantiate `clock.Default` (or a fresh `clock.New()`) and pass it into `Mount{Clock: ...}`.

Anywhere in production code that needs "now" for relative dates (Go side has none currently — it's all in frontend), inject `clock.Clock` rather than calling `time.Now`. For now no Go-side site needs the swap (relative time is rendered in TS); the test route still has value for **fixture loading**: `mockimap.Apply` and `seedHandler` should use `c.Now()` instead of `time.Now()` when no explicit Date is supplied.

- [ ] **Step 3: Update fixture loader to use clock**

In `internal/mockimap/seed.go` `buildRFC822`, accept a clock interface (`type now interface { Now() time.Time }`) and call `now.Now()` when `m.Date.IsZero()`. Pass the clock from `seedHandler`.

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/testapi/ ./internal/mockimap/ -v
git add internal/testapi/ internal/mockimap/ cmd/spk-mail/
git commit -m "feat(testapi): /api/_test/clock and clock-driven fixture timestamps"
```

---

## Task 4: Expanded db-dump

**Files:** modify `internal/testapi/dbdump.go`

- [ ] **Step 1: Replace handler**

```go
package testapi

import (
	"encoding/json"
	"net/http"

	"github.com/spk/spk-mail/internal/storage"
)

func dbDumpHandler(s *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		out := map[string]any{}
		accs, _ := s.ListAccounts(ctx);             out["accounts"] = accs
		threads, _ := s.ListThreadsRecent(ctx, 1000, 0); out["threads"]  = threads

		// folders + messages + attachments
		var folders []storage.FolderRow
		for _, a := range accs {
			fs, _ := s.ListFolders(ctx, a.ID)
			folders = append(folders, fs...)
		}
		out["folders"] = folders

		// flatten messages
		type msgDump struct {
			ID, FolderID, AccountID int64
			UID                     int64
			Subject, From, Flags    string
			Date                    int64
			HasAttachments          bool
		}
		var msgs []msgDump
		rows, _ := s.DB().QueryContext(ctx, `SELECT id,account_id,folder_id,uid,COALESCE(subject,''),COALESCE(from_addr,''),flags,date,has_attachments FROM messages ORDER BY date DESC LIMIT 1000`)
		defer rows.Close()
		for rows.Next() {
			var m msgDump; var hasAtt int
			_ = rows.Scan(&m.ID,&m.AccountID,&m.FolderID,&m.UID,&m.Subject,&m.From,&m.Flags,&m.Date,&hasAtt)
			m.HasAttachments = hasAtt != 0
			msgs = append(msgs, m)
		}
		out["messages"] = msgs

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/testapi/dbdump.go
git commit -m "feat(testapi): db-dump returns folders, messages, threads, accounts"
```

---

## Task 5: Build-tag separation for `_test` routes

**Files:** modify `cmd/spk-mail/main.go`

To ensure the desktop binary truly cannot expose `/api/_test/*`, mount them only behind an explicit build tag plus a runtime flag:

- [ ] **Step 1: Split `runBrowser` into a separate file**

Move `runBrowser` from `main.go` into `cmd/spk-mail/browser.go` with `//go:build !desktop_only`.

`cmd/spk-mail/browser.go`:
```go
//go:build !desktop_only

package main

// runBrowser, frontendFS embed, testapi mounting — exactly as in plan 1 + extensions added by later plans.
```

`cmd/spk-mail/browser_disabled.go`:
```go
//go:build desktop_only

package main

import (
	"context"
	"errors"
)

func runBrowser(_ context.Context, _ int, _ bool, _ string) error {
	return errors.New("browser mode disabled in this build (desktop_only)")
}
```

Update Makefile so the **release desktop build** uses `-tags desktop_only`:
```makefile
build-desktop: build-frontend
	mkdir -p $(BIN_DIR)
	rm -rf cmd/spk-mail/dist && cp -r frontend/dist cmd/spk-mail/dist
	CGO_ENABLED=1 go build -tags "wails desktop_only" -trimpath -ldflags="-w -s" -o $(BIN_DIR)/spk-mail-desktop ./cmd/spk-mail
	rm -rf cmd/spk-mail/dist
```

The default `build-go` (used by dev + CI for browser-mode tests) does NOT pass `desktop_only`, so it includes the `_test` routes.

- [ ] **Step 2: Verify**

```bash
make build-desktop
./build/bin/spk-mail-desktop --browser --port=5174 || echo "expected: browser mode disabled in this build"
```

Expected: error message, no listener bound.

- [ ] **Step 3: Commit**

```bash
git add cmd/spk-mail/ Makefile
git commit -m "feat: desktop_only build tag excludes browser mode and _test routes"
```

---

## Task 6: Additional fixtures

**Files:** `tests/fixtures/{multi-account,attachments,html-tracking}.yaml`

- [ ] **Step 1: multi-account fixture**

`tests/fixtures/multi-account.yaml`:
```yaml
accounts:
  - name: "Personal"
    email: "alice@example.com"
    color: "#3b82f6"
    use_mock: true
    folders:
      - name: INBOX
        messages:
          - from: "Bob <bob@example.com>"
            subject: "Project update"
            date: 2026-04-27T10:30:00Z
            body_text: "Here is the latest milestone."
  - name: "Work"
    email: "alice@work.example"
    color: "#10b981"
    use_mock: true
    folders:
      - name: INBOX
        messages:
          - from: "Carol <carol@work.example>"
            subject: "Q2 planning"
            date: 2026-04-26T08:00:00Z
            body_text: "Adding you to the planning thread."
```

- [ ] **Step 2: attachments fixture**

`tests/fixtures/attachments.yaml`:
```yaml
accounts:
  - name: "Test"
    email: "alice@example.com"
    color: "#a855f7"
    use_mock: true
    folders:
      - name: INBOX
        messages:
          - from: "Reports <reports@example.com>"
            subject: "Weekly digest"
            date: 2026-04-27T08:00:00Z
            body_html: "<h1>Top stories</h1><p>This week’s rundown…</p>"
            attachments:
              - filename: "report.pdf"
                content_type: "application/pdf"
                size: 102400
```

> **Implementer note:** the `mockimap.Apply` of plan 2 doesn't actually compose multipart MIME with binary attachment payloads. To make the attachment test deterministic, extend `buildRFC822` in `internal/mockimap/seed.go` to emit a `multipart/mixed` envelope with one base64-encoded part of `size` zero-bytes (`bytes.Repeat([]byte{0}, size)`) per attachment. Update plan-2 imports as needed; this is small.

- [ ] **Step 3: html-tracking fixture**

`tests/fixtures/html-tracking.yaml`:
```yaml
accounts:
  - name: "Marketing"
    email: "mkt@example.com"
    color: "#f59e0b"
    use_mock: true
    folders:
      - name: INBOX
        messages:
          - from: "newsletter@example.com"
            subject: "Special offer"
            date: 2026-04-27T09:00:00Z
            body_html: |
              <p>Hello! Check out our newest deal.</p>
              <img src="https://tracker.example.com/pixel?u=42" width="1" height="1" alt="">
              <a href="https://example.com/deal">See it</a>
```

- [ ] **Step 4: Commit**

```bash
git add tests/fixtures/ internal/mockimap/seed.go
git commit -m "test: multi-account, attachments, and html-tracking fixtures"
```

---

## Task 7: Reference Playwright suite

**Files:** `tests/playwright/{add-account,notification-flow,unblock-remote,threading,visual-regression}.spec.ts`

- [ ] **Step 1: add-account.spec.ts**

```ts
import { test, expect } from '@playwright/test'

test('user can add an account through the form', async ({ page, request }) => {
  await page.goto('/')
  await page.getByText('+ Add account').click()
  await page.getByLabel('Display name').fill('My Mail')
  await page.getByLabel('Email').fill('me@example.com')
  await page.getByLabel('IMAP host').fill('localhost')
  await page.getByLabel('App password').fill('whatever')
  await page.getByRole('button', { name: /add account/i }).click()
  // The mock IMAP server will reject this because the user wasn't pre-created;
  // we still expect the API call to register the row before the connect failure.
  await expect.poll(async () => {
    const dump = await (await request.get('/api/_test/db-dump')).json()
    return dump.accounts?.length ?? 0
  }).toBeGreaterThan(0)
})
```

- [ ] **Step 2: notification-flow.spec.ts**

```ts
import { test, expect } from '@playwright/test'

test('injecting a message updates the inbox via SSE', async ({ page, request }) => {
  await page.goto('/')
  // Subscribe to events from the page so we don't race
  await page.waitForSelector('text=Test Personal', { timeout: 10_000 })

  await request.post('/api/_test/inject-message', {
    data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Live update', body_text: 'hello live' }
  })

  await expect(page.getByText('Live update')).toBeVisible({ timeout: 10_000 })
})
```

- [ ] **Step 3: unblock-remote.spec.ts**

```ts
import { test, expect } from '@playwright/test'

test('blocked remote image becomes visible after unblock', async ({ page, request }) => {
  // Switch to the html-tracking fixture
  await request.post('/api/_test/seed', {
    data: { accounts: [{ name: 'Mkt', email: 'mkt@example.com', color: '#f59e0b', use_mock: true,
      folders: [{ name: 'INBOX', messages: [{ from: 'n@example.com', subject: 'Offer',
        date: '2026-04-27T09:00:00Z', body_html: '<img src="https://tracker.example/p.png">' }] }] }] }
  })
  await page.goto('/')
  await page.getByText('Offer').click()
  await page.getByRole('button', { name: /show remote content/i }).click()
  // The iframe srcDoc should now contain the original src
  const frame = page.frameLocator('iframe').first()
  await expect(frame.locator('img')).toHaveAttribute('src', /tracker\.example/)
})
```

- [ ] **Step 4: threading.spec.ts**

```ts
import { test, expect } from '@playwright/test'

test('reply lands in the same thread', async ({ page, request }) => {
  await page.goto('/')
  // Inject root
  await request.post('/api/_test/inject-message', { data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Topic A', body_text: 'first' } })
  // Inject reply with same subject + In-Reply-To headers (server constructs Message-IDs from time, so reuse subject for fallback subject-norm path)
  await request.post('/api/_test/inject-message', { data: { email: 'alice@example.com', from: 'Bob <b@x>', subject: 'Re: Topic A', body_text: 'second' } })

  await expect(page.getByText('Topic A')).toBeVisible({ timeout: 10_000 })
  await page.getByText('Topic A').click()
  // Both messages render
  await expect(page.getByText('first')).toBeVisible()
  await expect(page.getByText('second')).toBeVisible()
})
```

- [ ] **Step 5: visual-regression.spec.ts**

```ts
import { test, expect } from '@playwright/test'

test('clock-frozen screenshots of every page', async ({ page, request }) => {
  await request.post('/api/_test/clock', { data: { now: '2026-04-27T12:00:00Z' } })

  await page.goto('/'); await expect(page.getByText(/Test Personal/)).toBeVisible()
  await expect(page).toHaveScreenshot('inbox.png')

  await page.getByText(/Project update/).click()
  await expect(page).toHaveScreenshot('thread.png')

  await page.goto('/#/settings'); await expect(page.getByText(/Accounts/)).toBeVisible()
  await expect(page).toHaveScreenshot('settings.png')

  await request.post('/api/_test/clock', { data: { reset: true } })
})
```

- [ ] **Step 6: Run all + commit**

```bash
make build
cd tests/playwright && npx playwright test && cd ../..
git add tests/playwright/
git commit -m "test(ui): playwright suite covers add-account, IDLE, unblock-remote, threading, screenshots"
```

---

## Task 8: docs/ui-testing.md

**Files:** `docs/ui-testing.md`, `CLAUDE.md`

- [ ] **Step 1: Write the recipe book**

`docs/ui-testing.md`:
```markdown
# UI Testing Guide for Claude Code

This page is the entry point when you (Claude) are asked to verify, screenshot, or debug the spk-mail UI. The app exposes a localhost HTTP mode so the standard Playwright MCP works without any spk-mail-specific tooling.

## Quick start

    make build
    ./build/bin/spk-mail \
        --browser --port=5174 \
        --imap-mock \
        --seed=tests/fixtures/basic.yaml &
    # then drive: http://127.0.0.1:5174

When you're done:

    pkill -f 'spk-mail --browser'

## Available fixtures

| File | What it gives you |
|---|---|
| `tests/fixtures/basic.yaml` | One account with two messages — minimal sanity check |
| `tests/fixtures/multi-account.yaml` | Two accounts with different colors — exercises unified inbox |
| `tests/fixtures/attachments.yaml` | Message with a PDF attachment — tests download UI |
| `tests/fixtures/html-tracking.yaml` | HTML email with a remote tracking pixel — tests blocked-image flow |

## Test API endpoints (only present in `--browser` builds)

| Endpoint | Body | Purpose |
|---|---|---|
| `POST /api/_test/seed` | a `Fixture` JSON (same shape as the YAML fixtures) | Add accounts + messages at runtime |
| `POST /api/_test/inject-message` | `{email, from, subject, body_text}` | Trigger the IDLE → notification flow |
| `POST /api/_test/clock` | `{now: "RFC3339"}` or `{reset: true}` | Freeze "now" for deterministic screenshots |
| `GET  /api/_test/db-dump` | — | JSON snapshot of accounts/folders/messages/threads |
| `GET  /api/_test/logs` | — | Recent slog entries (in-memory ring buffer) |

These routes return 404 if you build with `-tags desktop_only` (the production desktop binary).

## Recipe — verify the new-message notification flow

```bash
# 1. Start the app with mock IMAP and a seeded account
./build/bin/spk-mail --browser --port=5174 --imap-mock --seed=tests/fixtures/basic.yaml &

# 2. Wait for boot (Playwright will do this automatically via webServer config)

# 3. Inject a message via the test API
curl -s -X POST http://127.0.0.1:5174/api/_test/inject-message \
    -H 'Content-Type: application/json' \
    -d '{"email":"alice@example.com","from":"Bob <b@x>","subject":"Hello","body_text":"hi"}'

# 4. Open the browser via Playwright MCP and assert the message shows up
```

In Playwright MCP form:

- `browser_navigate http://127.0.0.1:5174`
- `browser_wait_for text="Hello"` (with a timeout — IDLE flow takes <1s)
- `browser_take_screenshot` if visual confirmation is needed

## Recipe — visual regression run

1. `POST /api/_test/clock { now: "2026-04-27T12:00:00Z" }` — freezes relative-time strings.
2. Navigate every interesting URL: `/`, `/#/settings`, `/#/search?q=update`.
3. `browser_take_screenshot` on each; pass to `toHaveScreenshot()` in tests.
4. `POST /api/_test/clock { reset: true }` to release the freeze.

The committed Playwright spec `tests/playwright/visual-regression.spec.ts` already does this.

## Recipe — debug an event-handling bug

1. Open `/api/_test/logs` to see recent slog entries.
2. `browser_console_messages` to read frontend errors.
3. `browser_network_requests` to see which `/api/*` calls fired and their responses.
4. `GET /api/_test/db-dump` to confirm what made it into SQLite.

## Adding a new fixture

1. Drop the YAML under `tests/fixtures/`.
2. Reference it from a Playwright spec using `--seed=tests/fixtures/your.yaml` in `playwright.config.ts` or via `POST /api/_test/seed` at runtime.
3. Keep the file deterministic: explicit `date:` fields rather than relative ("now"); explicit colors and IDs; no random data.

## Caveats

- `--imap-mock` runs an in-process server; `Use TLS` should be `false` in any account configuration that points at it.
- The mock server resets when the binary restarts. To preserve state, persist via the on-disk DB (`XDG_DATA_HOME=…/data` set per run).
- Notifications (`MessageArrived` event) only fire for INBOX-role folders with `\\Seen` flag NOT set.
```

- [ ] **Step 2: Project CLAUDE.md**

`CLAUDE.md`:
```markdown
# Claude Code project guide

When you work on UI changes in spk-mail, read **`docs/ui-testing.md` first** — it documents how to launch the app in browser mode against an in-process mock IMAP server, the fixtures, and the `/api/_test/*` automation routes.

The end-to-end manual verification rule from the global CLAUDE.md applies here. For UI work, exercise the change in `--browser` mode (or via the Playwright suite under `tests/playwright/`) before reporting it as done.

Architecture and tech-stack details live in `docs/superpowers/specs/2026-04-27-spk-mail-design.md`.
```

- [ ] **Step 3: Commit**

```bash
git add docs/ui-testing.md CLAUDE.md
git commit -m "docs: ui-testing recipe book + project CLAUDE.md"
```

---

## Self-Review

**Spec coverage added by plan 7:**
- §11 the full UI-automation API: `clock`, expanded `db-dump`, slog→logs ring buffer, build-tag-gated `_test` routes — Tasks 1–5.
- §11 fixtures and recipes — Tasks 6, 7, 8.
- Project CLAUDE.md pointing future Claude sessions at the testing guide — Task 8.

**Gaps:** none — this plan completes the spec coverage. Items still deferred to follow-up (sending mail, OAuth, Windows/macOS, light theme) are explicitly out of scope per the spec.

**Type consistency:**
- The `Mount` struct gains a `Clock *clock.Clock` field; all callers updated together (Task 3) ✓.
- `LogEntry` JSON shape stable between handler, slog mirror, and frontend ✓ (frontend doesn't need a typed model — it just renders the JSON).
- `TestApi` route paths (`/api/_test/*`) match the documentation in `docs/ui-testing.md` ✓.

---

## Polish Backlog Accumulated During Plans 2–4

These items were flagged by code reviewers during plan 2 (sync engine), plan 3 (UI core), and plan 4 (tray + notifications) execution. They were deferred to plan 7 as plan-faithful tech debt or post-implementation polish. None are required for the spec end-state but most improve correctness, observability, or maintainability. Group them into the existing plan-7 task flow as fits, or make them a separate Task X "tech-debt sweep".

### Storage layer hygiene
- `FindThreadByMessageIDs` doc claims "case-insensitive" but SQL is case-sensitive — drop phrase or add COLLATE NOCASE.
- `UpdateThreadStats` LIKE patterns are unbounded substring matches — tighten to `'%"\Seen"%'` etc.
- `findThreadBySubject` proper `ErrNoRows` handling (currently any error → no match → spurious thread).
- Add `ORDER BY last_date DESC` to `findThreadBySubject` query for determinism.
- Surface `InsertAttachment` errors via `WriteError` event (currently swallowed).
- Per-message transaction wrapper in StoreWriter.process (orphan thread possible on InsertMessage failure).
- Move SQL escapes (`store.DB().ExecContext` in store_writer.go and stub.go) into typed methods: `Store.UpdateBodyHTML`, `Store.GetFolderByID`, `Store.FindThreadBySubject`, `Store.FolderRole`.

### MIME / sanitizer
- `walk()` control-flow comments documenting multipart-vs-leaf intent.
- Recursion-depth guard (max 32) in `walk` against deeply-nested attacker input.
- Document body-read best-effort contract on `Parse()`.
- Tighten `headerAddrFirst` fallback with `mail.ParseAddress`.
- Idempotency-safe `Sanitize` with hostile-injection defense — naive `AllowAttrs("data-spk-original-src")` is unsafe (creates attack vector via attacker pre-injecting the attr); proper fix needs strip-before-rewrite.
- Document `imgSrcPattern` post-bluemonday-canonical-input precondition.
- Add doc comment to `UnblockRemote`: "Input must be Sanitize() output."
- Fix double-space in rewritten `<img>` tags (cosmetic).
- Make `policy` private/unmutable via `sync.OnceValue` or local closure.
- `</body>` literal in body_html could terminate the iframe wrapper document early — escape on server or use srcDoc wrapper differently.

### Thread package
- Document `CandidateMessageIDs` non-deterministic map-iteration order (or `sort.Strings(out)` before return).
- More `NormalizeSubject` table cases: foreign prefixes (Antw/AW/Rv/Tr/Fw), "Hello: world" non-prefix.
- Test in-reply-to-in-references dedup case for `CandidateMessageIDs`.
- Drop redundant parens at jwz.go:47.

### IMAP wrapper
- ctx wiring on `Select`/`StoreFlags`/`Capabilities` — currently parameter is silently ignored.
- `SplitHostPort` returns `(string, int, error)` for safety in config-loader path (currently swallows port-parse error).
- `mailboxRole` precedence-ordered switch (currently first-match in iteration order).
- Split `\Archive` vs `\All` semantics (Gmail "All Mail" is conceptually different).
- Document `imap.Client` concurrency contract.
- `FetchedMessage.Internal` zero-time guard (currently produces `-6795364578871` for missing INTERNALDATE).
- `Capabilities` caching (currently re-issues CAPABILITY every call).
- Either remove `IdleNotification.UID` field (always 0) or strengthen docstring.
- `Idle()` re-entry guard (panic or warn if already running).
- Expand Fetch-handler `Collect()` latency comment to acknowledge worst-case memory cost on large literals.
- Exponential backoff for IDLE-error retry (currently fixed 2s).
- Hoist `refresh` timer out of Idle() loop using `Reset()`.
- Deduplicate `splitHostPort` (unexported) and `SplitHostPort` (exported).
- Investigate `TestIdle_FiresOnNewMessage` whole-repo flake (passes in isolation and under -race -count=10).

### Sync package
- Consider renaming `internal/sync` to `mailsync`/`syncengine` to avoid stdlib shadowing if alias tax becomes painful.
- Tighten `folderRegistry.Set(fid, validity, next)` signature (positional swap risk on identical int64 args) — pass `folderInfo` value or use named returns.
- Change `folderRegistry.state` map key from `FolderUID{FolderID: fid}` (UID always 0) to plain `int64`.
- Add `// Package sync …` doc comment.
- Convert `AccountStatus.State` to typed string with exported constants.
- Move `flagOps` drain loop into its own goroutine scoped to `Run` (decouples from runOnce restart so pending flag ops always drain).
- Supervisor-scoped IDLE/poll goroutine lifetime — currently leak across runOnce restarts.
- Extract `dialForAccount(ctx, acc) (*imap.Client, error)` helper to collapse three Dial sites in account_worker.go (runOnce/runIDLE/runPoll); also captures `secrets.Get` errors that are currently swallowed.
- Log/emit on runPoll/runIDLE error paths (currently silent).
- Eliminate `splitHostPortAddr` shim in account_worker.go.
- Exponential backoff in AccountWorker with auth-vs-network differentiation (currently fixed 5s).
- Fetch INBOX first then other folders in parallel.
- Assert `role="inbox"` uniqueness per account.
- Drop `var _ = imap.DialOpts{}` hedge in types.go (no longer needed once a real `imap.*` reference appears).
- Resync-suppression test case for StoreWriter (test that `IsResync: true` skips MessageArrived emit).
- Promote `contains`/`nilIfEmpty` to a shared util when AccountWorker also needs them.
- Engine double-spawn race on rapid Stop→Start for same id — track in-flight supervise via done channel; serialize Stop/Start.
- Reset Engine's `attempt` counter after a long-stable run before applying capped 300s.
- Hoist Engine's `delays` slice to package-level var for discoverability/test override.
- `engine_test.go::TestEngine_TwoAccountsSyncInParallel` should use `ctx + defer cancel()` for clean shutdown.

### API stub + main
- `MarkRead` early-continue when `\Seen` is already set (avoid redundant DB write/IMAP STORE/SSE event).
- `MarkRead` `slog.Warn` when `WorkerFor` returns nil — currently silent local-vs-server divergence.
- Skip `UpdateThreadStats` when `m.ThreadID` is nil (don't pass 0 silently).
- `Stub.AddAccount` should return `Status: "starting"` not `"ok"` (engine has not yet connected when the call returns).
- Promote `engineAdapter`/`workerAdapter` from `cmd/spk-mail/main.go` to `internal/sync/apiadapter` package before more transports need them.
- `slog.Warn` on JSON unmarshal failures in `GetThread`/`MarkRead` (currently silently degrade to empty arrays).
- Single transaction around `MarkRead` batch (or document partial-failure contract on the API method).
- `cmd/spk-mail/main.go::splitHostPort` use `strconv.Atoi` and surface error (currently silent port=0 on garbage input).
- Shutdown ordering: defer `eng.Run` ctx cancel + wait before `defer st.Close()` to avoid "sql: database is closed" warning at process exit.
- Engine on AddAccount duplicate-email — currently surfaces UNIQUE-constraint error from DB; either make Stub.AddAccount idempotent or runBrowser's seed loop should handle gracefully (currently `slog.Warn` only).

### testapi + e2e
- Extract `mockimap.BuildSimple(from, subject, body, when)` shared by `seed.go::buildSimple` and `inject.go` (currently divergent RFC822 builders).
- E2E tests bind `127.0.0.1:0` and parse port from binary log instead of hardcoded 5188/5189.
- Move `waitURL` to `tests/e2e/helpers_test.go` to avoid future symbol collision.
- Gate child stdio piping on `testing.Verbose()` for quiet CI runs.
- Fix two `defer resp.Body.Close()` defers in `idle_smoke_test.go` (one missing, one unreachable on success path).

### Frontend (plan 3)
- Server-side `/api/Search` JSON-tag bug — `internal/api/transport/http.go` request struct has `Limit`/`Offset` without JSON tags; client sends lowercase, decodes to 0. **Fix when plan 5 wires the search bar.**
- `go mod tidy` removes Wails v3 because the import is behind a build tag — must use `GOFLAGS="-tags=wails" go mod tidy` to keep it. Document in CONTRIBUTING.md or add a pre-commit guard.
- Wails-side JS event bridge — Go side emits via `app.Event.Emit(name, data)`, but the React frontend's `wailsClient.subscribeEvents` relies on a `window.wails.EventsOn` shape that may not match Wails v3's actual `@wailsio/runtime` API. Verify when desktop binary is exercised in real use.
- `useEventStream` empty-deps eslint warning — plan-faithful but worth `// eslint-disable-next-line` or stable-deps refactor.
- Wasteful `MessageInserted/Updated/Arrived` handler — refetches all threads + open thread on every event. Use store delta updates (`bumpThread`, `markThreadRead`) instead.
- Wasteful `AccountStatus` handler — refetches all accounts just to patch one status field.
- Frontend bundle size 204 kB — Zustand + React 19 only, no virtualization. Plan 3 calls ThreadList "virtualized" but verbatim code does not virtualize. Add react-window when thread counts grow.
- Playwright test relies on `XDG_DATA_HOME` isolation — without it, prior dev DB state collides. Document or make `runBrowser` always use a fresh DB in `--imap-mock` mode (since mock-port changes per launch anyway).
- `reuseExistingServer: !process.env.CI` — local dev re-runs without `CI=1` reuse stale servers. Document as a `make test-ui` make target that sets CI=1.

### Tray + Notifications (plan 4)
- `internal/api/stub.go` `UnreadCounts` SQL `flags NOT LIKE '%\Seen%'` is fragile — relies on JSON encoding producing `\\Seen` whose substring includes `\Seen`. Tighten to `NOT EXISTS (SELECT 1 FROM json_each(m.flags) WHERE value = '\Seen')` or move into a typed `Store.UnreadCountsByAccount` method (alongside the other escapes flagged in the Storage section).
- `internal/api/stub.go` `UnreadCounts` reaches into raw DB via `s.Store.DB().QueryContext(...)`; promote into a `Store.*` typed method to match the rest of the Stub.
- `internal/tray/tray.go` — `unread atomic.Int64` field is written but never read. Either expose via a getter (e.g. for a future "N unread" tooltip path) or remove.
- `internal/tray/tray.go` — `stop chan struct{}` is closed in `Close()` but never selected on. Either select on it inside `consume()` or drop the field.
- `internal/tray/tray.go` — exported `Close()` is never invoked from `internal/desktop/window.go`. Either `defer ctrl.Close()` in `desktop.Run` for clean shutdown, or drop the export.
- `internal/desktop/prompt.go` `openPasswordWindow` is a documented TODO stub. Implement the actual password window: either via a Wails frontend route/service callback (HTML form posts to a one-off Go service that resolves a channel), or via a native platform dialog if a future Wails alpha exposes one. After implementation, also wire `cmd/spk-mail/run_desktop_wails.go` to fall back to `desktop.PromptMasterPassword` when `secrets.LoadOrCreateMasterKey` returns `secrets.ErrKeyringUnavailable`.
- `internal/secrets/keyring.go` `DeriveKeyFromPassword` uses PBKDF2-HMAC-SHA256 (1M iters); plan 4 spec hinted at Argon2id. Consider migrating to Argon2id (RFC 9106 baseline parameters) when the password-prompt path is actually wired — at that point a one-time migration is needed to re-derive existing keys, so it's natural to do them together.
- `internal/tray/badge.go` `RenderBadge` circle math assumes a square icon (`r := bounds.Dx() / 4`, then `cx := bounds.Max.X - r - 1; cy := bounds.Min.Y + r + 1`). For a non-square icon the badge could clip the top edge or under-fill the right corner. Either document the square-icon precondition or compute `r := min(Dx,Dy)/4` and clamp coordinates.
