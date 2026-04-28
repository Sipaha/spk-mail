package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeleteMessagesByFolder simulates the UIDVALIDITY-reset path: messages
// stored under one UIDValidity are wiped before re-sync re-fetches them
// fresh. Without this delete, server-side UID reuse would silently corrupt
// the local cache.
func TestDeleteMessagesByFolder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 5})

	for i := int64(1); i <= 3; i++ {
		_, err := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: i, Date: 1700000000, Flags: `[]`})
		require.NoError(t, err)
	}

	require.NoError(t, s.DeleteMessagesByFolder(ctx, fID))

	var n int
	require.NoError(t, s.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE folder_id = ?`, fID).Scan(&n))
	require.Equal(t, 0, n)
}

// TestMaxUIDByFolder verifies the resume-from-DB-max cursor used by
// syncFolder so a partial bulk fetch survives a process restart without
// re-fetching messages already on disk.
func TestMaxUIDByFolder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	// Empty folder → 0.
	got, err := s.MaxUIDByFolder(ctx, fID)
	require.NoError(t, err)
	require.Equal(t, int64(0), got)

	for _, uid := range []int64{1, 5, 17, 12} {
		_, err := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: uid, Date: 1700000000, Flags: `[]`})
		require.NoError(t, err)
	}
	got, err = s.MaxUIDByFolder(ctx, fID)
	require.NoError(t, err)
	require.Equal(t, int64(17), got)
}

// TestInsertParsedMessageBundle_HappyPath exercises the all-in-one tx wrap
// of thread+message+attachment+stats and verifies the resulting rows are
// linked correctly.
func TestInsertParsedMessageBundle_HappyPath(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	msgID, threadID, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "topic", LastDate: 1700000000},
		Message: MessageRow{
			AccountID: accID, FolderID: fID, UID: 1, Date: 1700000000,
			Subject: stringPtr("topic"), Flags: `[]`,
			HasAttachments: true,
		},
		Attachments: []AttachmentRow{
			{PartID: "2", Filename: "a.pdf", ContentType: "application/pdf", SizeBytes: 100},
		},
	})
	require.NoError(t, err)
	require.Greater(t, msgID, int64(0))
	require.Greater(t, threadID, int64(0))

	atts, err := s.ListAttachmentsByMessage(ctx, msgID)
	require.NoError(t, err)
	require.Len(t, atts, 1)
	require.Equal(t, "a.pdf", atts[0].Filename)

	msg, err := s.GetMessage(ctx, msgID)
	require.NoError(t, err)
	require.NotNil(t, msg.ThreadID)
	require.Equal(t, threadID, *msg.ThreadID)
}

// TestInsertParsedMessageBundle_RollsBackOnFailure forces a duplicate-UID
// violation by submitting two bundles with the same (account, folder, UID)
// triple. The second submit must fail and leave NO orphan thread row from
// its rolled-back NewThread insert.
func TestInsertParsedMessageBundle_RollsBackOnFailure(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	// First bundle succeeds: 1 thread + 1 message in DB.
	_, firstThreadID, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "first", LastDate: 1700000000},
		Message: MessageRow{
			AccountID: accID, FolderID: fID, UID: 1, Date: 1700000000, Flags: `[]`,
		},
	})
	require.NoError(t, err)

	// Second bundle reuses (account, folder, UID=1) → UNIQUE violation
	// inside the tx. The bundle's NewThread insert must roll back so we
	// don't accumulate an orphan thread on every duplicate.
	_, _, err = s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "second-orphan", LastDate: 1700000001},
		Message: MessageRow{
			AccountID: accID, FolderID: fID, UID: 1, Date: 1700000001, Flags: `[]`,
		},
	})
	require.Error(t, err)

	// Only the first thread should exist — no rolled-back orphan.
	threads, _ := s.ListThreadsRecent(ctx, 100, 0)
	require.Len(t, threads, 1)
	require.Equal(t, firstThreadID, threads[0].ID)
}
