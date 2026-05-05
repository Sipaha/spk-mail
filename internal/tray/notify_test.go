package tray

import (
	"os"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

// TestNotifier_Smoke fires a real notification through the user's session
// D-Bus daemon. That's a visible side effect on the developer's desktop, so
// it stays gated behind an explicit env var — otherwise every `go test ./...`
// run would pop a "spk-mail test / ok" toast.
//
//	SPK_NOTIFIER_SMOKE=1 go test ./internal/tray/ -run TestNotifier_Smoke
func TestNotifier_Smoke(t *testing.T) {
	if os.Getenv("SPK_NOTIFIER_SMOKE") == "" {
		t.Skip("set SPK_NOTIFIER_SMOKE=1 to run the live D-Bus notify smoke test")
	}
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	n, err := NewNotifier()
	require.NoError(t, err)
	id, err := n.Notify("spk-mail test", "ok", []string{"default", "Open"})
	require.NoError(t, err)
	require.NotZero(t, id)
}

// TestNotifier_DispatchSignal_RoutesActionInvoked covers the
// hot-path translation from a dbus signal to the registered
// ActionHandler — the bit that wires "user clicked the toast" to the
// rest of the app. dbus is mocked by constructing a *dbus.Signal
// directly; no live bus required.
func TestNotifier_DispatchSignal_RoutesActionInvoked(t *testing.T) {
	n := &Notifier{}
	var gotID uint32
	var gotAction string
	n.SetActionHandler(func(id uint32, action string) {
		gotID = id
		gotAction = action
	})
	n.dispatchSignal(&dbus.Signal{
		Name: actionSignal,
		Body: []any{uint32(42), "default"},
	})
	require.Equal(t, uint32(42), gotID)
	require.Equal(t, "default", gotAction)
}

// TestNotifier_DispatchSignal_RoutesNotificationClosed mirrors the
// ActionInvoked test for the close signal — used by the controller to
// evict pending-action entries on dismissal.
func TestNotifier_DispatchSignal_RoutesNotificationClosed(t *testing.T) {
	n := &Notifier{}
	var gotID, gotReason uint32
	n.SetCloseHandler(func(id, reason uint32) {
		gotID = id
		gotReason = reason
	})
	n.dispatchSignal(&dbus.Signal{
		Name: closeSignal,
		Body: []any{uint32(7), uint32(2)},
	})
	require.Equal(t, uint32(7), gotID)
	require.Equal(t, uint32(2), gotReason)
}

// TestNotifier_DispatchSignal_NoHandlerIsSafe locks in that signals
// arriving before SetActionHandler/SetCloseHandler don't panic — the
// init order in tray.NewController calls NewNotifier first and only
// later registers handlers.
func TestNotifier_DispatchSignal_NoHandlerIsSafe(t *testing.T) {
	n := &Notifier{}
	require.NotPanics(t, func() {
		n.dispatchSignal(&dbus.Signal{Name: actionSignal, Body: []any{uint32(1), "default"}})
		n.dispatchSignal(&dbus.Signal{Name: closeSignal, Body: []any{uint32(1), uint32(2)}})
	})
}

// TestNotifier_DispatchSignal_MalformedBodyIgnored guards against a
// rogue daemon emitting a signal whose body doesn't match the spec —
// we drop it silently rather than panicking on a type assertion.
func TestNotifier_DispatchSignal_MalformedBodyIgnored(t *testing.T) {
	n := &Notifier{}
	called := false
	n.SetActionHandler(func(uint32, string) { called = true })
	n.SetCloseHandler(func(uint32, uint32) { called = true })

	require.NotPanics(t, func() {
		n.dispatchSignal(&dbus.Signal{Name: actionSignal, Body: []any{"not-a-uint32"}})
		n.dispatchSignal(&dbus.Signal{Name: actionSignal, Body: []any{uint32(1), 99}})
		n.dispatchSignal(&dbus.Signal{Name: closeSignal, Body: []any{uint32(1)}})
		n.dispatchSignal(&dbus.Signal{Name: "org.freedesktop.Notifications.Unknown", Body: []any{uint32(1)}})
	})
	require.False(t, called, "handlers must not fire for malformed signals")
}
