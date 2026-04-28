package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupTwoProfilesTwoAccounts(t *testing.T) (*Store, struct {
	Work, Personal           int64
	AccW, AccP               int64
	INBOX_W, INBOX_P, Sent_W int64
	TFlag, TUnread, TRead    int64 // thread ids
}) {
	s := openTestStore(t)
	ctx := context.Background()
	var ids struct {
		Work, Personal           int64
		AccW, AccP               int64
		INBOX_W, INBOX_P, Sent_W int64
		TFlag, TUnread, TRead    int64
	}

	ids.Work, _ = s.InsertProfile(ctx, ProfileRow{Name: "Work", Color: "#10b981", SortOrder: 0, CreatedAt: 0})
	ids.Personal, _ = s.InsertProfile(ctx, ProfileRow{Name: "Personal", Color: "#3b82f6", SortOrder: 1, CreatedAt: 0})

	ids.AccW, _ = s.InsertAccount(ctx, AccountRow{Name: "W", Email: "w@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &ids.Work})
	ids.AccP, _ = s.InsertAccount(ctx, AccountRow{Name: "P", Email: "p@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0, ProfileID: &ids.Personal})

	role := "inbox"
	sentRole := "sent"
	ids.INBOX_W, _ = s.UpsertFolder(ctx, FolderRow{AccountID: ids.AccW, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})
	ids.Sent_W, _ = s.UpsertFolder(ctx, FolderRow{AccountID: ids.AccW, Name: "Sent", Delimiter: "/", Role: &sentRole, UIDValidity: 1, UIDNext: 1})
	ids.INBOX_P, _ = s.UpsertFolder(ctx, FolderRow{AccountID: ids.AccP, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	// Thread A: 1 message in Work/INBOX, has_flagged=1, unread.
	ids.TFlag, _ = s.InsertThread(ctx, ThreadRow{SubjectNorm: "flag", LastDate: 100, MsgCount: 1, UnreadCount: 1, HasFlagged: true})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: ids.AccW, FolderID: ids.INBOX_W, UID: 1, ThreadID: &ids.TFlag, Subject: stringPtr("flag"), Date: 100, Flags: `["\\Flagged"]`})

	// Thread B: 1 message in Work/Sent, unread, NOT flagged.
	ids.TUnread, _ = s.InsertThread(ctx, ThreadRow{SubjectNorm: "unread", LastDate: 200, MsgCount: 1, UnreadCount: 1})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: ids.AccW, FolderID: ids.Sent_W, UID: 1, ThreadID: &ids.TUnread, Subject: stringPtr("unread"), Date: 200, Flags: `[]`})

	// Thread C: 1 message in Personal/INBOX, READ, NOT flagged.
	ids.TRead, _ = s.InsertThread(ctx, ThreadRow{SubjectNorm: "read", LastDate: 300, MsgCount: 1, UnreadCount: 0})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: ids.AccP, FolderID: ids.INBOX_P, UID: 1, ThreadID: &ids.TRead, Subject: stringPtr("read"), Date: 300, Flags: `["\\Seen"]`})

	return s, ids
}

func TestListThreads_EmptyFilterReturnsAll(t *testing.T) {
	s, _ := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 3)
}

func TestListThreads_FilterByProfile(t *testing.T) {
	s, ids := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{ProfileID: &ids.Work}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2) // TFlag + TUnread
}

func TestListThreads_FilterByAccount(t *testing.T) {
	s, ids := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{AccountID: &ids.AccP}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1) // TRead
}

func TestListThreads_FilterByFolder(t *testing.T) {
	s, ids := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{FolderID: &ids.Sent_W}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1) // TUnread
}

func TestListThreads_FilterUnreadOnly(t *testing.T) {
	s, _ := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{UnreadOnly: true}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2) // TFlag + TUnread
}

func TestListThreads_FilterHasFlagged(t *testing.T) {
	s, _ := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{HasFlagged: true}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1) // TFlag
}

func TestListThreads_AND_ProfileAndFolder(t *testing.T) {
	s, ids := setupTwoProfilesTwoAccounts(t)
	rows, err := s.ListThreads(context.Background(), ThreadFilter{ProfileID: &ids.Work, FolderID: &ids.Sent_W}, 50, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1) // TUnread
}

func TestMessageCountsByFolder(t *testing.T) {
	s, ids := setupTwoProfilesTwoAccounts(t)
	counts, err := s.MessageCountsByFolder(context.Background(), ids.AccW)
	require.NoError(t, err)
	// Work/INBOX has 1 flagged unread message.
	require.Equal(t, FolderCounts{Total: 1, Unread: 1, Flagged: 1}, counts[ids.INBOX_W])
	// Work/Sent has 1 plain unread message.
	require.Equal(t, FolderCounts{Total: 1, Unread: 1, Flagged: 0}, counts[ids.Sent_W])

	countsP, err := s.MessageCountsByFolder(context.Background(), ids.AccP)
	require.NoError(t, err)
	// Personal/INBOX has 1 read-only message.
	require.Equal(t, FolderCounts{Total: 1, Unread: 0, Flagged: 0}, countsP[ids.INBOX_P])
}

func TestMessageCountsByFolder_FullMatrix(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	role := "inbox"
	fid, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fid, UID: 1, Date: 1, Flags: `[]`})                            // unread, not flagged
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fid, UID: 2, Date: 2, Flags: `["\\Seen"]`})                    // read,   not flagged
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fid, UID: 3, Date: 3, Flags: `["\\Flagged"]`})                 // unread, flagged
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fid, UID: 4, Date: 4, Flags: `["\\Seen","\\Flagged"]`})        // read,   flagged

	counts, err := s.MessageCountsByFolder(ctx, accID)
	require.NoError(t, err)
	require.Equal(t, FolderCounts{Total: 4, Unread: 2, Flagged: 2}, counts[fid])
}
