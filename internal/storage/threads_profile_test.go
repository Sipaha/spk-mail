package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListThreads_FiltersByProfile(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	work, _ := s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 0})
	personal, _ := s.InsertProfile(ctx, ProfileRow{Name: "Personal", Color: "#3b82f6", SortOrder: 1, CreatedAt: 0})

	accW, _ := s.InsertAccount(ctx, AccountRow{
		Name: "W", Email: "w@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &work,
	})
	accP, _ := s.InsertAccount(ctx, AccountRow{
		Name: "P", Email: "p@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &personal,
	})

	role := "inbox"
	fW, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accW, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})
	fP, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accP, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	tW, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "work", LastDate: 100})
	tP, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "personal", LastDate: 200})

	_, err := s.InsertMessage(ctx, MessageRow{AccountID: accW, FolderID: fW, UID: 1, ThreadID: &tW, Subject: stringPtr("WorkSubj"), Date: 100, Flags: "[]"})
	require.NoError(t, err)
	_, err = s.InsertMessage(ctx, MessageRow{AccountID: accP, FolderID: fP, UID: 1, ThreadID: &tP, Subject: stringPtr("PersonalSubj"), Date: 200, Flags: "[]"})
	require.NoError(t, err)

	all, err := s.ListThreads(ctx, ThreadFilter{}, 50, 0)
	require.NoError(t, err)
	require.Len(t, all, 2)

	wp := work
	wonly, err := s.ListThreads(ctx, ThreadFilter{ProfileID: &wp}, 50, 0)
	require.NoError(t, err)
	require.Len(t, wonly, 1)
	require.Equal(t, "work", wonly[0].SubjectNorm)
}
