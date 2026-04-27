package tray

import (
	"errors"
	"sync"

	"github.com/godbus/dbus/v5"
)

// Notifier delivers desktop notifications via the org.freedesktop.Notifications
// D-Bus interface.
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

// Notify shows a desktop notification. AppName "spk-mail", icon name "mail-message-new",
// urgency 1 (Normal). Returns the notification id.
func (n *Notifier) Notify(summary, body string) (uint32, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn == nil {
		return 0, errors.New("dbus not connected")
	}
	obj := n.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0,
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
