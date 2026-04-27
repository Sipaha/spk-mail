package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfiles_CRUD(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// Default profile from migration backfill is absent because the DB has
	// no accounts; profiles list is empty.
	all, err := s.ListProfiles(ctx)
	require.NoError(t, err)
	require.Empty(t, all)

	id1, err := s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 1700000000})
	require.NoError(t, err)
	id2, err := s.InsertProfile(ctx, ProfileRow{Name: "Personal", Color: "#3b82f6", SortOrder: 1, CreatedAt: 1700000001})
	require.NoError(t, err)

	all, _ = s.ListProfiles(ctx)
	require.Len(t, all, 2)
	require.Equal(t, "Work", all[0].Name) // sort_order 0 first

	require.NoError(t, s.UpdateProfile(ctx, id1, "Office", "#ef4444"))
	row, err := s.GetProfile(ctx, id1)
	require.NoError(t, err)
	require.Equal(t, "Office", row.Name)
	require.Equal(t, "#ef4444", row.Color)

	require.NoError(t, s.DeleteProfile(ctx, id2))
	all, _ = s.ListProfiles(ctx)
	require.Len(t, all, 1)
}

func TestProfiles_DeleteRefusedWhenAccountsAttached(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	pid, _ := s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 0})
	_, err := s.InsertAccount(ctx, AccountRow{
		Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &pid,
	})
	require.NoError(t, err)

	err = s.DeleteProfile(ctx, pid)
	require.ErrorIs(t, err, ErrProfileInUse)
}
