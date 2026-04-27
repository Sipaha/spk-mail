package tray

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNotifier_Smoke(t *testing.T) {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		t.Skip("no session bus")
	}
	n, err := NewNotifier()
	require.NoError(t, err)
	id, err := n.Notify("spk-mail test", "ok")
	require.NoError(t, err)
	require.NotZero(t, id)
}
