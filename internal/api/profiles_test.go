package api

import (
	"context"
	"errors"
	"testing"

	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestProfiles_AddListUpdateDelete(t *testing.T) {
	a := newStub(t)
	ctx := context.Background()

	p, err := a.AddProfile(ctx, AddProfileRequest{Name: "Work", Color: "#10b981"})
	require.NoError(t, err)
	require.NotZero(t, p.ID)
	require.Equal(t, "Work", p.Name)

	all, err := a.ListProfiles(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	updated, err := a.UpdateProfile(ctx, UpdateProfileRequest{ID: p.ID, Name: "Office", Color: "#ef4444"})
	require.NoError(t, err)
	require.Equal(t, "Office", updated.Name)

	require.NoError(t, a.DeleteProfile(ctx, p.ID))
	all, _ = a.ListProfiles(ctx)
	require.Empty(t, all)
}

func TestProfiles_DeleteInUseSurfacesSentinel(t *testing.T) {
	a := newStub(t)
	ctx := context.Background()
	p, _ := a.AddProfile(ctx, AddProfileRequest{Name: "Work", Color: "#10b981"})
	_, err := a.AddAccount(ctx, AddAccountRequest{
		Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "secret", UseTLS: true, Color: "#fff",
		ProfileID: &p.ID,
	})
	require.NoError(t, err)
	err = a.DeleteProfile(ctx, p.ID)
	require.Error(t, err)
	require.True(t,
		errors.Is(err, ErrProfileInUse) || errors.Is(err, storage.ErrProfileInUse),
		"expected ErrProfileInUse, got %v", err)
}

func TestAddAccount_PersistsProfileID(t *testing.T) {
	a := newStub(t)
	ctx := context.Background()
	p, _ := a.AddProfile(ctx, AddProfileRequest{Name: "Work", Color: "#10b981"})
	acc, err := a.AddAccount(ctx, AddAccountRequest{
		Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "secret", UseTLS: true, Color: "#fff",
		ProfileID: &p.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, acc.ProfileID)
	require.Equal(t, p.ID, *acc.ProfileID)

	all, _ := a.ListAccounts(ctx)
	require.Len(t, all, 1)
	require.NotNil(t, all[0].ProfileID)
	require.Equal(t, p.ID, *all[0].ProfileID)
}

func TestProfiles_Mute_TogglesAndExcludesFromTotal(t *testing.T) {
	a := newStub(t)
	ctx := context.Background()

	p, err := a.AddProfile(ctx, AddProfileRequest{Name: "Work", Color: "#10b981"})
	require.NoError(t, err)
	require.False(t, p.Muted)

	acc, err := a.AddAccount(ctx, AddAccountRequest{
		Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "secret", UseTLS: true, Color: "#fff",
		ProfileID: &p.ID,
	})
	require.NoError(t, err)

	role := "inbox"
	fID, err := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: acc.ID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	_, err = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: acc.ID, FolderID: fID, UID: 1, Date: 100, Flags: "[]"})
	require.NoError(t, err)

	total, err := a.TotalUnreadExcludingMuted(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)

	require.NoError(t, a.SetProfileMuted(ctx, p.ID, true))
	total, _ = a.TotalUnreadExcludingMuted(ctx)
	require.Equal(t, int64(0), total)

	muted, _ := a.AccountIsMuted(ctx, acc.ID)
	require.True(t, muted)

	all, _ := a.ListProfiles(ctx)
	require.Len(t, all, 1)
	require.True(t, all[0].Muted)
}
