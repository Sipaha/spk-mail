//go:build wails

package tray

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/api"
)

// Controller wires the system tray (icon + menu + tooltip) to api.Emitter
// events: it shows desktop notifications for newly arrived mail and refreshes
// the unread badge whenever account state changes.
type Controller struct {
	app        *application.App
	api        api.API
	emitter    *api.Emitter
	baseIcon   []byte // neutral state (no unread)
	unreadIcon []byte // accent state (unread > 0); falls back to baseIcon if nil
	wnd        *application.WebviewWindow

	tray     *application.SystemTray
	notifier *Notifier

	unread atomic.Int64

	once  sync.Once
	unsub func()
}

// NewController constructs the tray, registers the menu, subscribes to events
// and starts a goroutine that processes them. Errors from notifier construction
// are logged but non-fatal; everything else is best-effort.
//
// `unreadIcon` may be nil — in that case the tray uses `baseIcon` for both
// states and only the numeric badge overlay differentiates them.
func NewController(
	app *application.App,
	a api.API,
	emitter *api.Emitter,
	icon []byte,
	unreadIcon []byte,
	wnd *application.WebviewWindow,
) (*Controller, error) {
	c := &Controller{
		app:        app,
		api:        a,
		emitter:    emitter,
		baseIcon:   icon,
		unreadIcon: unreadIcon,
		wnd:        wnd,
	}

	notifier, err := NewNotifier()
	if err != nil {
		log.Printf("tray: notifier unavailable: %v", err)
	} else {
		c.notifier = notifier
	}

	c.tray = app.SystemTray.New()
	c.tray.SetIcon(icon)
	c.tray.SetTooltip("spk-mail — no new mail")

	menu := application.NewMenu()
	menu.Add("Show spk-mail").OnClick(func(*application.Context) {
		c.showWindow()
	})
	menu.Add("Settings").OnClick(func(*application.Context) {
		c.showWindow()
		if c.wnd != nil {
			c.wnd.SetURL("/#/settings")
		}
	})
	menu.AddSeparator()
	menu.Add("Quit").OnClick(func(*application.Context) {
		c.app.Quit()
	})
	c.tray.SetMenu(menu)
	c.tray.OnClick(c.raiseToFront)

	ch, unsub := emitter.Subscribe()
	c.unsub = unsub
	go c.consume(ch)

	// Prime the badge with the current unread total.
	go c.refreshUnread()

	return c, nil
}

// Close stops the event subscription. Safe to call multiple times.
// `consume` exits when the subscription channel closes (driven by `unsub`),
// so there is no separate stop signal to manage.
func (c *Controller) Close() {
	c.once.Do(func() {
		if c.unsub != nil {
			c.unsub()
		}
	})
}

// raiseToFront makes the window visible (Show if Hidden, Restore if
// minimised) and forces it above other windows.
//
// The SetAlwaysOnTop(true) / SetAlwaysOnTop(false) wrap around Focus() works
// around Linux WM focus-stealing-prevention: gtk_window_present alone (which
// is what Wails' Focus() does on Linux — see webview_window_linux.go::focus
// → present → gtk_window_present) is treated by Mutter/KWin/Xfwm/Sway as a
// background request without a user-activation timestamp, and the window
// shows up stacked behind whatever currently has focus. The keep-above hint
// (gtk_window_set_keep_above) is an ICCCM/EWMH window-state, unconditionally
// honored — toggling it true → present → false reliably brings the window
// forward without leaving it pinned.
//
// Each call goes through InvokeSync to the GTK main loop, so the four
// operations serialise in the right order.
func (c *Controller) raiseToFront() {
	if c.wnd == nil {
		return
	}
	switch {
	case !c.wnd.IsVisible():
		c.wnd.Show()
	case c.wnd.IsMinimised():
		c.wnd.Restore()
	}
	c.wnd.SetAlwaysOnTop(true)
	c.wnd.Focus()
	c.wnd.SetAlwaysOnTop(false)
}

func (c *Controller) showWindow() {
	c.raiseToFront()
}

func (c *Controller) consume(ch <-chan api.Event) {
	for ev := range ch {
		switch ev.Type {
		case "MessageArrived":
			// Hand off to a goroutine: AccountIsMuted is a SQLite read
			// and Notify is a dbus call with a 5s timeout. Running them
			// inline here would block the consume loop, which is the
			// only drainer of a bounded (cap=64) subscriber channel.
			// Two MessageArrived events landing while the dbus call is
			// in flight would otherwise overflow into the channel-drop
			// path and the user would silently miss notifications.
			ev := ev
			go c.handleMessageArrived(ev)
			c.refreshUnread()
		case "MessageInserted", "MessageUpdated", "AccountStatus", "FolderMarkedRead":
			c.refreshUnread()
		}
	}
}

// handleMessageArrived runs the notify side-effect off the consume
// goroutine — see the comment in consume() for why decoupling matters.
// Logs at INFO so missing notifications can be diagnosed from the
// in-memory log buffer / journalctl: every fired path leaves a trail.
func (c *Controller) handleMessageArrived(ev api.Event) {
	if c.notifier == nil {
		log.Printf("tray: MessageArrived received but notifier is nil (dbus init failed at startup)")
		return
	}
	// account_id may arrive as int64 (in-process Emitter) or float64
	// (JSON round-trip via Wails event bus); accept both.
	var accID int64
	switch v := ev.Payload["account_id"].(type) {
	case int64:
		accID = v
	case float64:
		accID = int64(v)
	}
	muted := false
	if accID > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		m, err := c.api.AccountIsMuted(ctx, accID)
		cancel()
		if err != nil {
			log.Printf("tray: AccountIsMuted failed: %v (treating as not muted)", err)
		} else {
			muted = m
		}
	}
	if muted {
		return
	}
	from, _ := ev.Payload["from"].(string)
	subject, _ := ev.Payload["subject"].(string)
	if _, err := c.notifier.Notify("New mail · "+from, subject); err != nil {
		log.Printf("tray: notify failed: %v (account_id=%d from=%q)", err, accID, from)
	}
}

func (c *Controller) refreshUnread() {
	total, err := c.api.TotalUnreadExcludingMuted(context.Background())
	if err != nil {
		log.Printf("tray: TotalUnreadExcludingMuted failed: %v", err)
		return
	}
	prev := c.unread.Load()
	c.unread.Store(total)
	// Log only on transitions so steady-state ticks don't spam the
	// journal, but every change leaves a breadcrumb. A "no unread
	// shown after new mail arrives" report can be triaged by
	// looking for this line: if total stayed at 0, the SQL count
	// itself is the culprit (probably a folder.role mis-detection
	// or messages.flags rewrite); if total bumped but the icon
	// didn't visually update, the issue is in tray.SetIcon /
	// RenderBadge.
	if prev != total {
		log.Printf("tray: unread count %d -> %d", prev, total)
	}

	// Pick base icon by state. Unread > 0 → accent (blue) variant, so the tray
	// stripe pops out of the desktop chrome at a glance even before the user
	// looks at the numeric badge. Falls back to the neutral icon if no unread
	// variant was provided at construction time.
	base := c.baseIcon
	if total > 0 && c.unreadIcon != nil {
		base = c.unreadIcon
	}

	badge, err := RenderBadge(base, int(total))
	if err != nil {
		log.Printf("tray: RenderBadge failed: %v", err)
		badge = base
	}
	c.tray.SetIcon(badge)
	c.tray.SetTooltip("spk-mail — " + tooltipText(int(total)))
}

func tooltipText(n int) string {
	switch {
	case n <= 0:
		return "no new mail"
	case n == 1:
		return "1 unread"
	default:
		return fmt.Sprintf("%d unread", n)
	}
}
