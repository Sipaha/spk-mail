package api

import (
	"context"
	"testing"

	"github.com/spk/spk-mail/internal/events"
	"github.com/spk/spk-mail/internal/teststore"
	"github.com/stretchr/testify/require"
)

// testStub returns a *Stub backed by teststore.Open (nil engine — unit tests
// don't exercise sync).
func testStub(t *testing.T) *Stub {
	t.Helper()
	st, sec := teststore.Open(t)
	return NewStub(st, sec, events.NewEmitter(), nil)
}

func TestStub_AddListRemoveAccount(t *testing.T) {
	st := testStub(t)
	ctx := context.Background()
	out, err := st.AddAccount(ctx, AddAccountRequest{Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", IMAPPassword: "p", UseTLS: true, Color: "#fff"})
	require.NoError(t, err)
	require.Greater(t, out.ID, int64(0))

	list, _ := st.ListAccounts(ctx)
	require.Len(t, list, 1)

	require.NoError(t, st.RemoveAccount(ctx, out.ID))
	list, _ = st.ListAccounts(ctx)
	require.Empty(t, list)
}
