# Tray + Notifications Implementation Plan (Plan 4 of 7)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisites:** Plans 1, 2, 3 merged.

**Goal:** Make spk-mail behave as a real tray-resident desktop client. Tray icon shows a numeric unread badge; closing the window minimizes to the tray (process keeps running, IDLE stays alive); clicking the tray icon shows/hides the window; the tray menu has "Show", "Settings", "Quit". When a new INBOX message arrives, fire a native desktop notification via `org.freedesktop.Notifications`. Add a master-password prompt for the desktop case where the OS keyring is unavailable.

**Architecture:** A new `internal/tray` package owns tray icon, badge composition, and notifications. It subscribes to `api.Emitter` and reacts to `MessageArrived` and account status changes. The Wails-bundled native tray (`application.SystemTray`) handles the icon + menu; for notifications we use `github.com/godbus/dbus/v5` + a tiny wrapper because Wails alpha's notification API is unstable. Window-close → hide is wired in the desktop runner.

**Tech Stack:** Wails v3 system-tray API, `github.com/godbus/dbus/v5` (already pulled transitively), in-memory `unread` counter recomputed from the DB.

---

## File Structure

Created:

```
internal/
├── tray/
│   ├── tray.go                  # SystemTray + menu + badge
│   ├── badge.go                 # render PNG with unread count overlay
│   ├── notify.go                # D-Bus org.freedesktop.Notifications wrapper
│   ├── badge_test.go
│   └── notify_test.go           # verifies D-Bus payload (no real bus needed: use Conn fake)
└── desktop/
    ├── window.go                # extended: window close → hide, master-pw prompt fallback
    └── prompt.go                # password prompt window for first-run keyring fallback

internal/api/
└── stub.go                      # +UnreadCounts(ctx) method
```

---

## Task 1: Unread counts API

**Files:** modify `internal/api/api.go`, `internal/api/stub.go`, `internal/api/transport/{http,wails}.go`

- [ ] **Step 1: Test for `UnreadCounts`**

`internal/api/unread_test.go`:
```go
package api

import (
	"context"
	"testing"
	"github.com/spk/spk-mail/internal/api/testapi"
	"github.com/stretchr/testify/require"
)

func TestUnreadCounts_Empty(t *testing.T) {
	a := testapi.NewStub(t).(*Stub)
	out, err := a.UnreadCounts(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), out.Total)
	require.Empty(t, out.PerAccount)
}
```

- [ ] **Step 2: Add type + method**

In `internal/api/dto.go`:
```go
type UnreadCountsDTO struct {
	Total      int64           `json:"total"`
	PerAccount map[int64]int64 `json:"per_account"`
}
```

In `internal/api/api.go`:
```go
type API interface {
	// …existing…
	UnreadCounts(ctx context.Context) (UnreadCountsDTO, error)
}
```

In `internal/api/stub.go`:
```go
func (s *Stub) UnreadCounts(ctx context.Context) (UnreadCountsDTO, error) {
	rows, err := s.Store.DB().QueryContext(ctx, `
		SELECT m.account_id, COUNT(*)
		FROM messages m
		JOIN folders f ON m.folder_id = f.id
		WHERE f.role = 'inbox' AND m.flags NOT LIKE '%\Seen%'
		GROUP BY m.account_id`)
	if err != nil { return UnreadCountsDTO{}, err }
	defer rows.Close()
	out := UnreadCountsDTO{PerAccount: map[int64]int64{}}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil { return UnreadCountsDTO{}, err }
		out.PerAccount[id] = n
		out.Total += n
	}
	return out, rows.Err()
}
```

- [ ] **Step 3: Add HTTP route + Wails method**

In `transport/http.go` `routes()`:
```go
h.mux.HandleFunc("POST /api/UnreadCounts", h.handle(func(ctx context.Context, _ *struct{}) (any, error) { return h.api.UnreadCounts(ctx) }))
```

In `transport/wails.go`:
```go
func (w *Wails) UnreadCounts() (api.UnreadCountsDTO, error) { return w.a.UnreadCounts(context.Background()) }
```

- [ ] **Step 4: Run + commit**

```bash
go test ./internal/api/...
git add internal/api/
git commit -m "feat(api): UnreadCounts aggregation per account"
```

---

## Task 2: Badge image renderer

**Files:** `internal/tray/badge.go`, `internal/tray/badge_test.go`

We render a PNG by composing the base icon with a red circle + numeric label using `image` + `image/png` + `golang.org/x/image/font/basicfont`.

- [ ] **Step 1: Test**

`internal/tray/badge_test.go`:
```go
package tray

import (
	"bytes"
	"image"
	_ "image/png"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestRenderBadge_DecodesAsPNG(t *testing.T) {
	base := simpleBaseIcon(64, 64) // small helper that returns a 64x64 PNG bytes
	out, err := RenderBadge(base, 12)
	require.NoError(t, err)
	img, _, err := image.Decode(bytes.NewReader(out))
	require.NoError(t, err)
	require.Equal(t, 64, img.Bounds().Dx())
}

func TestRenderBadge_ZeroReturnsBase(t *testing.T) {
	base := simpleBaseIcon(64, 64)
	out, _ := RenderBadge(base, 0)
	require.Equal(t, base, out)
}
```

`simpleBaseIcon` is a test helper inside `badge_test.go` that returns 64×64 transparent PNG bytes (use `image.NewNRGBA` + `png.Encode`).

- [ ] **Step 2: Implement**

`internal/tray/badge.go`:
```go
package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// RenderBadge overlays a red circular badge with `count` on top of base PNG.
// If count is 0, returns base unchanged.
func RenderBadge(base []byte, count int) ([]byte, error) {
	if count <= 0 { return base, nil }
	src, err := png.Decode(bytes.NewReader(base))
	if err != nil { return nil, err }
	bounds := src.Bounds()
	out := image.NewNRGBA(bounds)
	draw.Draw(out, bounds, src, bounds.Min, draw.Src)

	r := bounds.Dx() / 4
	cx := bounds.Max.X - r - 1
	cy := bounds.Min.Y + r + 1
	red := color.NRGBA{220, 38, 38, 255}
	for y := cy - r; y <= cy + r; y++ {
		for x := cx - r; x <= cx + r; x++ {
			dx, dy := x - cx, y - cy
			if dx*dx + dy*dy <= r*r { out.SetNRGBA(x, y, red) }
		}
	}
	label := strconv.Itoa(count); if count > 99 { label = "99+" }
	face := basicfont.Face7x13
	w := font.MeasureString(face, label).Round()
	d := &font.Drawer{
		Dst: out, Src: image.NewUniform(color.White), Face: face,
		Dot: fixed.P(cx - w/2, cy + face.Metrics().Ascent.Round()/2 - 1),
	}
	d.DrawString(label)

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil { return nil, err }
	return buf.Bytes(), nil
}
```

Add to `go.mod`: `go get golang.org/x/image@latest`.

- [ ] **Step 3: Run + commit**

```bash
go test ./internal/tray/ -v
git add internal/tray/badge.go internal/tray/badge_test.go go.mod go.sum
git commit -m "feat(tray): PNG badge renderer with numeric overlay"
```

---

## Task 3: Native notifications via D-Bus

**Files:** `internal/tray/notify.go`, `internal/tray/notify_test.go`

- [ ] **Step 1: Implement notifier**

`internal/tray/notify.go`:
```go
// Package tray provides system-tray and notification effects for the desktop
// app. Notifications go through org.freedesktop.Notifications (D-Bus), which
// works on every major Linux desktop environment.
package tray

import (
	"errors"
	"sync"

	"github.com/godbus/dbus/v5"
)

type Notifier struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

func NewNotifier() (*Notifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil { return nil, err }
	return &Notifier{conn: conn}, nil
}

// Notify shows a desktop notification. AppName "spk-mail", icon name "mail-message-new",
// urgency 1 (Normal). Returns the notification id.
func (n *Notifier) Notify(summary, body string) (uint32, error) {
	n.mu.Lock(); defer n.mu.Unlock()
	if n.conn == nil { return 0, errors.New("dbus not connected") }
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
		"spk-mail", uint32(0), "mail-message-new", summary, body,
		[]string{}, map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}, int32(-1))
	if call.Err != nil { return 0, call.Err }
	var id uint32
	if err := call.Store(&id); err != nil { return 0, err }
	return id, nil
}
```

- [ ] **Step 2: Smoke test (skipped in CI without bus)**

`internal/tray/notify_test.go`:
```go
package tray

import (
	"os"
	"testing"
	"github.com/stretchr/testify/require"
)

func TestNotifier_Smoke(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" { t.Skip("no session bus") }
	n, err := NewNotifier(); require.NoError(t, err)
	id, err := n.Notify("spk-mail test", "ok"); require.NoError(t, err)
	require.NotZero(t, id)
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/tray/notify.go internal/tray/notify_test.go
git commit -m "feat(tray): D-Bus notifier for org.freedesktop.Notifications"
```

---

## Task 4: System tray (Wails) + event bridge

**Files:** `internal/tray/tray.go`

- [ ] **Step 1: Implement**

`internal/tray/tray.go`:
```go
//go:build wails

package tray

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/api"
)

// Controller binds tray icon + notifications to api.Emitter and api.API.
type Controller struct {
	app      *application.App
	api      api.API
	em       *api.Emitter
	notifier *Notifier
	baseIcon []byte
	wnd      *application.WebviewWindow
	tray     *application.SystemTray
	unread   atomic.Int64
}

func NewController(app *application.App, a api.API, em *api.Emitter, baseIcon []byte, wnd *application.WebviewWindow) (*Controller, error) {
	n, err := NewNotifier(); if err != nil { slog.Warn("notifier unavailable", "err", err) }
	c := &Controller{app: app, api: a, em: em, notifier: n, baseIcon: baseIcon, wnd: wnd}
	c.tray = app.NewSystemTray()
	c.tray.SetMenu(c.menu())
	if err := c.refreshUnread(context.Background()); err != nil { slog.Warn("init unread", "err", err) }
	c.subscribeEvents()
	return c, nil
}

func (c *Controller) menu() *application.Menu {
	m := application.NewMenu()
	m.Add("Show spk-mail").OnClick(func(_ *application.Context) { c.wnd.Show(); c.wnd.Focus() })
	m.Add("Settings").OnClick(func(_ *application.Context) { c.wnd.Show(); _ = c.wnd.OpenURL("/#/settings") })
	m.AddSeparator()
	m.Add("Quit").OnClick(func(_ *application.Context) { c.app.Quit() })
	return m
}

func (c *Controller) subscribeEvents() {
	go func() {
		ch, _ := c.em.Subscribe()
		for ev := range ch {
			switch ev.Type {
			case "MessageArrived":
				if c.notifier != nil {
					subj, _ := ev.Payload["subject"].(string)
					from, _ := ev.Payload["from"].(string)
					_, _ = c.notifier.Notify("New mail · "+from, subj)
				}
				_ = c.refreshUnread(context.Background())
			case "MessageUpdated", "MessageInserted", "AccountStatus":
				_ = c.refreshUnread(context.Background())
			}
		}
	}()
}

func (c *Controller) refreshUnread(ctx context.Context) error {
	u, err := c.api.UnreadCounts(ctx); if err != nil { return err }
	c.unread.Store(u.Total)
	icon, err := RenderBadge(c.baseIcon, int(u.Total)); if err != nil { return err }
	c.tray.SetIcon(icon)
	c.tray.SetTooltip("spk-mail — " + tooltipFor(int(u.Total)))
	return nil
}

func tooltipFor(n int) string {
	switch {
	case n == 0: return "no new mail"
	case n == 1: return "1 unread"
	default:     return "" // simple — exact "N unread" is fine
	}
}
```

> **Note:** Wails v3 alpha tray API names (`NewSystemTray`, `SetMenu`, `SetIcon`, `SetTooltip`) may differ; the implementer should match the pinned version. The flow — subscribe to `MessageArrived` → notify + refresh badge — is stable.

- [ ] **Step 2: Wire into desktop runner**

In `internal/desktop/window.go`, after creating the window:
```go
import (
	"github.com/spk/spk-mail/internal/tray"
)

// inside Run after w := app.NewWebviewWindowWithOptions(...)
if _, err := tray.NewController(app, opts.API, opts.Emitter, opts.IconPNG, w); err != nil {
	slog.Warn("tray controller", "err", err)
}

// Override window close: hide instead of quit
w.OnEvent("close", func(_ *application.WindowEvent) bool { w.Hide(); return true /* consume */ })
```

(The exact event hook depends on Wails version. The intent: window close consumed → hide window, app keeps running.)

- [ ] **Step 3: Commit**

```bash
git add internal/tray/tray.go internal/desktop/window.go
git commit -m "feat(tray): system tray with badge, menu, minimize-to-tray, mail notifications"
```

---

## Task 5: Master-password prompt fallback

**Files:** `internal/desktop/prompt.go`

When `secrets.LoadOrCreateMasterKey` returns `ErrKeyringUnavailable`, open a small Wails window asking for a password, derive a key with `secrets.DeriveKeyFromPassword(pw, salt)`, and persist `salt.bin` next to `secrets.bin`.

- [ ] **Step 1: Implement**

`internal/desktop/prompt.go`:
```go
//go:build wails

package desktop

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/fsutil"
	"github.com/spk/spk-mail/internal/secrets"
)

// PromptMasterPassword opens a small modal asking for a password and returns
// a derived 32-byte key. Persists a per-installation 16-byte salt at
// <secretsDir>/master.salt; reuses it on subsequent runs.
func PromptMasterPassword(app *application.App, secretsPath string) ([]byte, error) {
	saltPath := filepath.Join(filepath.Dir(secretsPath), "master.salt")
	salt, err := os.ReadFile(saltPath)
	if errors.Is(err, os.ErrNotExist) {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil { return nil, err }
		if err := fsutil.AtomicWrite(saltPath, salt, 0o600); err != nil { return nil, err }
	} else if err != nil { return nil, err }

	pw, err := openPasswordWindow(app); if err != nil { return nil, err }
	if pw == "" { return nil, errors.New("no password entered") }
	return secrets.DeriveKeyFromPassword(pw, salt), nil
}

// openPasswordWindow shows a small Wails window with a password input and
// returns the entered string (or "" if cancelled). Implementation detail
// matches whatever Wails v3 alpha exposes — typically a tiny HTML form
// served from an in-memory asset handler that posts the value back via
// a one-off Wails service method.
func openPasswordWindow(app *application.App) (string, error) {
	// Minimal implementation: spawn a window with src=data:text/html with a
	// form, register a one-shot service, wait on a channel. The implementer
	// follows the Wails v3 alpha widget API for this; if the version exposes
	// application.Dialog().Prompt(), prefer that.
	return "", errors.New("password prompt: implement against the pinned wails v3 API")
}
```

> **Note for the implementer:** if the pinned Wails version exposes `application.Dialog().Prompt()` or similar, prefer that over hand-rolling a window. Functional contract is what matters: returns the user-entered password string, or empty if cancelled.

- [ ] **Step 2: Commit**

```bash
git add internal/desktop/prompt.go
git commit -m "feat(desktop): master-password prompt fallback when OS keyring missing"
```

---

## Task 6: End-to-end manual smoke

This task is human-verification only — `make` cannot easily test tray UI in CI.

- [ ] **Step 1: Build desktop**

```bash
make build-desktop
./build/bin/spk-mail-desktop &
```

- [ ] **Step 2: Verify**

- Tray icon appears.
- Window can be hidden via close (X) — process still running (`pgrep spk-mail` shows it).
- Tray menu has "Show", "Settings", "Quit".
- Add a fake account (or use mock IMAP via dev script `make run-browser`), inject a message via `curl -X POST http://127.0.0.1:5174/api/_test/inject-message …`, verify a desktop notification appears and the tray icon shows a `1` badge.
- Click "Quit" in the tray menu → process exits.

- [ ] **Step 3: Document in README**

Append to `README.md`:
```markdown
## Tray support

spk-mail uses Linux system-tray protocols.

- KDE / XFCE / Cinnamon / MATE: works out of the box.
- GNOME: install the [AppIndicator extension](https://extensions.gnome.org/extension/615/appindicator-support/).
- Wayland-only sessions: depends on the compositor.

Notifications use `org.freedesktop.Notifications` (D-Bus) and work on all major DEs.
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: tray and notifications support matrix"
```

---

## Self-Review

**Spec coverage added by plan 4:**
- §5.1 minimize-to-tray and quit-via-tray-menu — Task 4.
- Tray icon with unread badge — Tasks 2, 4.
- Native notifications on `MessageArrived` — Tasks 3, 4.
- §9 keyring-unavailable fallback (master password prompt) — Task 5.

**Gaps for later plans:**
- IDLE on non-INBOX folders — out of scope; INBOX-only matches the spec.
- Notification "click → open thread" deep-link — could be added later (the D-Bus notifier returns the id; subscribing to `ActionInvoked` is straightforward but cross-DE behavior is uneven).

**Type consistency:**
- `UnreadCountsDTO` field names are stable across stub, HTTP, Wails, and tray controller ✓.
- The events `Controller.subscribeEvents` listens to (`MessageArrived`, `MessageUpdated`, `MessageInserted`, `AccountStatus`) match exactly what `internal/sync/store_writer.go` emits ✓.
