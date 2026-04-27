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

func TestProfiles_MuteFlag(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	pid, _ := s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 0})

	got, _ := s.GetProfile(ctx, pid)
	require.False(t, got.Muted)

	require.NoError(t, s.SetProfileMuted(ctx, pid, true))
	got, _ = s.GetProfile(ctx, pid)
	require.True(t, got.Muted)

	require.NoError(t, s.SetProfileMuted(ctx, pid, false))
	got, _ = s.GetProfile(ctx, pid)
	require.False(t, got.Muted)
}

func TestTotalUnreadExcludingMuted(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	work, _ := s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 0})
	personal, _ := s.InsertProfile(ctx, ProfileRow{Name: "Personal", Color: "#3b82f6", SortOrder: 1, CreatedAt: 0})

	accW, _ := s.InsertAccount(ctx, AccountRow{Name: "W", Email: "w@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &work})
	accP, _ := s.InsertAccount(ctx, AccountRow{Name: "P", Email: "p@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &personal})

	role := "inbox"
	fW, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accW, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})
	fP, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accP, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	_, err := s.InsertMessage(ctx, MessageRow{AccountID: accW, FolderID: fW, UID: 1, Date: 100, Flags: "[]"})
	require.NoError(t, err)
	_, err = s.InsertMessage(ctx, MessageRow{AccountID: accP, FolderID: fP, UID: 1, Date: 200, Flags: "[]"})
	require.NoError(t, err)

	total, err := s.TotalUnreadExcludingMuted(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)

	require.NoError(t, s.SetProfileMuted(ctx, personal, true))
	total, _ = s.TotalUnreadExcludingMuted(ctx)
	require.Equal(t, int64(1), total)

	muted, err := s.AccountIsMuted(ctx, accP)
	require.NoError(t, err)
	require.True(t, muted)

	muted, _ = s.AccountIsMuted(ctx, accW)
	require.False(t, muted)
}
