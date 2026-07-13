package sync

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDialAccount_OpensSession verifies that DialAccount returns a
// usable IMAP client wired with the account's stored credentials.
func TestDialAccount_OpensSession(t *testing.T) {
	fx := setupMockAccount(t, "alice@example.com", "secret")

	c, err := DialAccount(context.Background(), fx.Store, fx.Secrets, fx.AccID)
	require.NoError(t, err)
	defer c.Close()
}
