//go:build wails

package tray

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/api"
)

// refreshInterval is the backstop cadence for refreshUnread. Events are
// the primary update path; this ticker exists so a single dropped /
// panicked / silently-failed SetIcon doesn't wedge the badge until the
// next process restart. 30s keeps the load trivial (one COUNT(*) query)
// while still recovering from an overnight stall in under a minute.
const refreshInterval = 30 * time.Second

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
	pending  *pendingActions

	unread atomic.Int64

	once  sync.Once
	unsub func()
	stop  chan struct{}
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
		pending:    newPendingActions(0),
		stop:       make(chan struct{}),
	}

	notifier, err := NewNotifier()
	if err != nil {
		log.Printf("tray: notifier unavailable: %v", err)
	} else {
		c.notifier = notifier
		// Wire click-to-open: when the daemon reports the user clicked
		// the notification body (or a registered action), look up the
		// thread we stashed against this notification id and route the
		// window to it. SetCloseHandler keeps the pending map bounded
		// even when daemons emit only NotificationClosed (no
		// ActionInvoked) on dismissal.
		notifier.SetActionHandler(c.onNotificationAction)
		notifier.SetCloseHandler(func(id uint32, _ uint32) {
			c.pending.Delete(id)
		})
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
	go c.tickRefresh()

	// Prime the badge with the current unread total.
	go c.refreshUnread()

	return c, nil
}

// Close stops the event subscription and the periodic refresh ticker.
// Safe to call multiple times. `consume` exits when the subscription
// channel closes (driven by `unsub`); `tickRefresh` exits on `stop`.
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stop)
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
		c.dispatchEvent(ev)
	}
}

// dispatchEvent isolates a single event behind a recover so a panic in
// SetIcon / RenderBadge / dbus does not kill the consume goroutine and
// permanently wedge the badge. The next event still gets a fair shot.
func (c *Controller) dispatchEvent(ev api.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray: dispatch panic: %v (event=%s)", r, ev.Type)
		}
	}()
	switch ev.Type {
	case "MessageArrived":
		// Hand off to a goroutine: AccountIsMuted is a SQLite read
		// and Notify is a dbus call with a 5s timeout. Running them
		// inline here would block the consume loop, which is the
		// only drainer of a bounded (cap=64) subscriber channel.
		// Two MessageArrived events landing while the dbus call is
		// in flight would otherwise overflow into the channel-drop
		// path and the user would silently miss notifications.
		go c.handleMessageArrived(ev)
		c.refreshUnread()
	case "MessageInserted", "MessageUpdated", "AccountStatus", "FolderMarkedRead":
		c.refreshUnread()
	}
}

// tickRefresh is a backstop for refreshUnread: the badge update is
// otherwise wholly event-driven, and any single failure mode in that
// path (subscriber-channel overflow, panic in SetIcon, dbus hiccup
// after suspend/resume) leaves the count stuck until process restart.
// Periodic refresh papers over all of those without trying to
// distinguish them.
func (c *Controller) tickRefresh() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray: tickRefresh panic: %v", r)
		}
	}()
	t := time.NewTicker(refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.refreshUnread()
			slog.Debug("tray: periodic refresh", "unread", c.unread.Load())
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
	accID := payloadInt64(ev.Payload, "account_id")
	threadID := payloadInt64(ev.Payload, "thread_id")
	msgID := payloadInt64(ev.Payload, "id")

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

	// "default" is the org.freedesktop.Notifications convention for
	// the body click. The "Open" label is shown by daemons that render
	// actions as buttons (KDE Plasma); on dunst/GNOME Shell the label
	// is unused but the body click still routes here.
	actions := []string{"default", "Open"}
	id, err := c.notifier.Notify("New mail · "+from, subject, actions)
	if err != nil {
		log.Printf("tray: notify failed: %v (account_id=%d from=%q)", err, accID, from)
		return
	}
	if threadID > 0 {
		c.pending.Put(id, ActionContext{
			AccountID: accID,
			ThreadID:  threadID,
			MessageID: msgID,
		})
	}
}

// onNotificationAction is the callback registered with the Notifier:
// when the daemon emits ActionInvoked for a notification id we posted,
// look up the originating thread and route the window to it. Runs on
// the Notifier's signal-consumer goroutine, so the wails calls go
// through InvokeSync (raiseToFront / SetURL both already do this for
// us).
func (c *Controller) onNotificationAction(id uint32, _ string) {
	ctx, ok := c.pending.Take(id)
	if !ok {
		return
	}
	c.raiseToFront()
	if c.wnd != nil && ctx.ThreadID > 0 {
		c.wnd.SetURL(fmt.Sprintf("/#/thread/%d", ctx.ThreadID))
	}
}

// payloadInt64 accepts both int64 (in-process Emitter) and float64
// (JSON round-trip via the Wails event bus) — same dual-shape coercion
// the tray already does for account_id.
func payloadInt64(p map[string]any, key string) int64 {
	switch v := p[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

func (c *Controller) refreshUnread() {
	total, err := c.api.TotalUnreadExcludingMuted(context.Background())
	if err != nil {
		log.Printf("tray: TotalUnreadExcludingMuted failed: %v", err)
		return
	}
	c.unread.Store(total)

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
