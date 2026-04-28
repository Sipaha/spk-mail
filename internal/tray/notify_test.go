package tray

import (
	"os"
	"testing"

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
	id, err := n.Notify("spk-mail test", "ok")
	require.NoError(t, err)
	require.NotZero(t, id)
}
