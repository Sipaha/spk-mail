//go:build wails

package tray

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-mail/internal/api"
)

// Controller wires the system tray (icon + menu + tooltip) to api.Emitter
// events: it shows desktop notifications for newly arrived mail and refreshes
// the unread badge whenever account state changes.
type Controller struct {
	app      *application.App
	api      api.API
	emitter  *api.Emitter
	baseIcon []byte
	wnd      *application.WebviewWindow

	tray     *application.SystemTray
	notifier *Notifier

	unread atomic.Int64

	once  sync.Once
	unsub func()
	stop  chan struct{}
}

// NewController constructs the tray, registers the menu, subscribes to events
// and starts a goroutine that processes them. Errors from notifier construction
// are logged but non-fatal; everything else is best-effort.
func NewController(
	app *application.App,
	a api.API,
	emitter *api.Emitter,
	icon []byte,
	wnd *application.WebviewWindow,
) (*Controller, error) {
	c := &Controller{
		app:      app,
		api:      a,
		emitter:  emitter,
		baseIcon: icon,
		wnd:      wnd,
		stop:     make(chan struct{}),
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
	c.tray.OnClick(c.toggleWindow)

	ch, unsub := emitter.Subscribe()
	c.unsub = unsub
	go c.consume(ch)

	// Prime the badge with the current unread total.
	go c.refreshUnread()

	return c, nil
}

// Close stops the event subscription. Safe to call multiple times.
func (c *Controller) Close() {
	c.once.Do(func() {
		if c.unsub != nil {
			c.unsub()
		}
		close(c.stop)
	})
}

func (c *Controller) showWindow() {
	if c.wnd == nil {
		return
	}
	c.wnd.Show()
	c.wnd.Focus()
}

// toggleWindow is wired to the tray icon's left-click. It hides the window if
// it's currently visible, otherwise shows and focuses it. This gives users a
// one-click way to recover the window after the close-to-tray hook hides it.
func (c *Controller) toggleWindow() {
	if c.wnd == nil {
		return
	}
	if c.wnd.IsVisible() {
		c.wnd.Hide()
		return
	}
	c.wnd.Show()
	c.wnd.Focus()
}

func (c *Controller) consume(ch <-chan api.Event) {
	for ev := range ch {
		switch ev.Type {
		case "MessageArrived":
			from, _ := ev.Payload["from"].(string)
			subject, _ := ev.Payload["subject"].(string)
			if c.notifier != nil {
				if _, err := c.notifier.Notify("New mail · "+from, subject); err != nil {
					log.Printf("tray: notify failed: %v", err)
				}
			}
			c.refreshUnread()
		case "MessageInserted", "MessageUpdated", "AccountStatus":
			c.refreshUnread()
		}
	}
}

func (c *Controller) refreshUnread() {
	counts, err := c.api.UnreadCounts(context.Background())
	if err != nil {
		log.Printf("tray: UnreadCounts failed: %v", err)
		return
	}
	total := counts.Total
	c.unread.Store(total)

	badge, err := RenderBadge(c.baseIcon, int(total))
	if err != nil {
		log.Printf("tray: RenderBadge failed: %v", err)
		badge = c.baseIcon
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
