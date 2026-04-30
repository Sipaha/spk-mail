package tray

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// Notifier delivers desktop notifications via the org.freedesktop.Notifications
// D-Bus interface.
//
// The connection is reconnect-aware: if a Notify call returns a connection-
// level error (broken pipe, "dbus connection closed"), the next call
// transparently re-dials the session bus. Without this the notifier would
// silently no-op for the rest of the process lifetime after a single
// transient session-bus glitch (logind restart, lock-screen suspend resume).
type Notifier struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

// NewNotifier connects to the session bus and returns a ready Notifier.
func NewNotifier() (*Notifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, err
	}
	return &Notifier{conn: conn}, nil
}

// Notify shows a desktop notification. AppName "spk-mail", icon name
// "mail-message-new", urgency 1 (Normal). Returns the notification id.
//
// Bounded by a 5-second context: a wedged notification daemon (e.g. a
// just-crashed dunst that systemd is restarting) used to block the
// caller indefinitely because godbus's default Call has no deadline,
// which froze the tray's event-consume loop and caused subsequent
// MessageArrived events to be dropped on the bounded subscriber
// channel. With the timeout the notify just returns an error after 5s
// and the consumer keeps draining.
func (n *Notifier) Notify(summary, body string) (uint32, error) {
	id, err := n.callNotify(summary, body)
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
	return n.callNotify(summary, body)
}

func (n *Notifier) callNotify(summary, body string) (uint32, error) {
	n.mu.Lock()
	conn := n.conn
	n.mu.Unlock()
	if conn == nil {
		return 0, errors.New("dbus not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		"spk-mail", uint32(0), "mail-message-new", summary, body,
		[]string{}, map[string]dbus.Variant{"urgency": dbus.MakeVariant(byte(1))}, int32(-1))
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
