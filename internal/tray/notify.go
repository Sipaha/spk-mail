package tray

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	notifyIface  = "org.freedesktop.Notifications"
	notifyPath   = "/org/freedesktop/Notifications"
	actionSignal = notifyIface + ".ActionInvoked"
	closeSignal  = notifyIface + ".NotificationClosed"
)

// ActionHandler is invoked when a notification daemon reports that the
// user activated one of the actions declared in the Notify call.
// `action` is the action key (the odd-indexed entries of the actions
// list passed to Notify); for the default click it is "default".
type ActionHandler func(notificationID uint32, action string)

// CloseHandler is invoked when the daemon dismisses a notification —
// either by user (reason=2), expiry (reason=1), or programmatic close
// (reason=3). Used to evict stale pending-action entries.
type CloseHandler func(notificationID uint32, reason uint32)

// Notifier delivers desktop notifications via the org.freedesktop.Notifications
// D-Bus interface and routes inbound ActionInvoked/NotificationClosed
// signals back to the application through optional handlers.
//
// The connection is reconnect-aware: if a Notify call returns a connection-
// level error (broken pipe, "dbus connection closed"), the next call
// transparently re-dials the session bus. Without this the notifier would
// silently no-op for the rest of the process lifetime after a single
// transient session-bus glitch (logind restart, lock-screen suspend resume).
//
// Signal subscription is set up once in NewNotifier and intentionally not
// re-attached on reconnect. godbus's SessionBus() returns a cached
// process-wide connection, so the typical "reconnect" actually reuses the
// same wire and the original match rule is still in effect; covering the
// rare hard-disconnect case would mean tracking the consumer goroutine and
// re-subscribing, which is more moving parts than the failure mode
// warrants. If signals do go away after a hard reconnect, click-to-open
// silently degrades — desktop notifications themselves keep firing.
type Notifier struct {
	mu       sync.Mutex
	conn     *dbus.Conn
	onAction ActionHandler
	onClose  CloseHandler
}

// NewNotifier connects to the session bus, subscribes to action/close
// signals on the Notifications interface, and returns a ready Notifier.
// Failure to subscribe to signals is logged but non-fatal: notifications
// will still fire, but ActionInvoked won't reach SetActionHandler.
func NewNotifier() (*Notifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	n := &Notifier{conn: conn}
	if err := n.attachSignals(conn); err != nil {
		log.Printf("notify: signal subscribe failed: %v (clicks won't open mail)", err)
	}
	return n, nil
}

// SetActionHandler registers (or replaces) the callback fired when the
// notification daemon emits ActionInvoked. Safe to call from any goroutine.
func (n *Notifier) SetActionHandler(h ActionHandler) {
	n.mu.Lock()
	n.onAction = h
	n.mu.Unlock()
}

// SetCloseHandler registers (or replaces) the callback fired when the
// notification daemon emits NotificationClosed. Safe to call from any
// goroutine.
func (n *Notifier) SetCloseHandler(h CloseHandler) {
	n.mu.Lock()
	n.onClose = h
	n.mu.Unlock()
}

// Notify shows a desktop notification. AppName "spk-mail", icon name
// "mail-message-new", urgency 1 (Normal). `actions` is a flat list of
// (key, label) pairs; pass nil for a non-actionable notification. To
// make clicking the notification body navigate, include "default" as a
// key — daemons treat the "default" action as the body click.
//
// Returns the notification id assigned by the daemon. The caller can
// use that id to correlate later ActionInvoked/NotificationClosed
// callbacks with the originating context.
//
// Bounded by a 5-second context: a wedged notification daemon (e.g. a
// just-crashed dunst that systemd is restarting) used to block the
// caller indefinitely because godbus's default Call has no deadline,
// which froze the tray's event-consume loop and caused subsequent
// MessageArrived events to be dropped on the bounded subscriber
// channel. With the timeout the notify just returns an error after 5s
// and the consumer keeps draining.
func (n *Notifier) Notify(summary, body string, actions []string) (uint32, error) {
	id, err := n.callNotify(summary, body, actions)
	if err == nil {
		return id, nil
	}
	// Reconnect-and-retry once on a connection-level failure. Higher-
	// level errors (rate limit, malformed args) bubble straight back
	// to the caller; only "the bus pipe is broken" deserves a redial.
	if !isConnectionError(err) {
		return 0, err
	}
	if rErr := n.reconnect(); rErr != nil {
		return 0, rErr
	}
	return n.callNotify(summary, body, actions)
}

func (n *Notifier) callNotify(summary, body string, actions []string) (uint32, error) {
	n.mu.Lock()
	conn := n.conn
	n.mu.Unlock()
	if conn == nil {
		return 0, errors.New("dbus not connected")
	}
	if actions == nil {
		actions = []string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	obj := conn.Object(notifyIface, notifyPath)
	call := obj.CallWithContext(ctx, notifyIface+".Notify", 0,
		"spk-mail", uint32(0), "mail-message-new", summary, body,
		actions, map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}, int32(-1))
	if call.Err != nil {
		return 0, call.Err
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (n *Notifier) reconnect() error {
	conn, err := dbus.SessionBus()
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.conn = conn
	n.mu.Unlock()
	return nil
}

func (n *Notifier) attachSignals(conn *dbus.Conn) error {
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(notifyIface),
	); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)
	go func() {
		for sig := range ch {
			n.dispatchSignal(sig)
		}
	}()
	return nil
}

// dispatchSignal routes a single dbus signal to the registered handler.
// Extracted from the consumer goroutine so unit tests can drive it
// directly with handcrafted signals.
func (n *Notifier) dispatchSignal(sig *dbus.Signal) {
	if sig == nil {
		return
	}
	switch sig.Name {
	case actionSignal:
		if len(sig.Body) < 2 {
			return
		}
		id, idOK := sig.Body[0].(uint32)
		action, actOK := sig.Body[1].(string)
		if !idOK || !actOK {
			return
		}
		n.mu.Lock()
		h := n.onAction
		n.mu.Unlock()
		if h != nil {
			h(id, action)
		}
	case closeSignal:
		if len(sig.Body) < 2 {
			return
		}
		id, idOK := sig.Body[0].(uint32)
		reason, reasonOK := sig.Body[1].(uint32)
		if !idOK || !reasonOK {
			return
		}
		n.mu.Lock()
		h := n.onClose
		n.mu.Unlock()
		if h != nil {
			h(id, reason)
		}
	}
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Cheap substring match — godbus doesn't export typed errors for
	// these cases. False positives just trigger an extra reconnect,
	// which is harmless.
	return contains(s, "closed") || contains(s, "broken pipe") ||
		contains(s, "EOF") || contains(s, "connection")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
