package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAttachments_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	mID, _ := s.InsertMessage(ctx, MessageRow{AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]"})

	id, err := s.InsertAttachment(ctx, AttachmentRow{MessageID: mID, PartID: "1.2", Filename: "x.pdf", ContentType: "application/pdf", SizeBytes: 100})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := s.ListAttachmentsByMessages(ctx, []int64{mID})
	require.NoError(t, err)
	require.Len(t, got[mID], 1)
}

func TestListAttachmentsByMessages_Batch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	// One account → one folder → three messages, attachments scattered:
	//   m1 → 2 attachments
	//   m2 → 0 attachments (must be absent from result map)
	//   m3 → 1 attachment
	//   non-existent ID 9999 → absent from result
	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)
	mkMsg := func(uid int64) int64 {
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid, Flags: "[]",
		})
		require.NoError(t, err)
		return id
	}
	m1, m2, m3 := mkMsg(1), mkMsg(2), mkMsg(3)

	mkAtt := func(msgID int64, part, name string) int64 {
		id, err := s.InsertAttachment(ctx, AttachmentRow{
			MessageID: msgID, PartID: part, Filename: name,
			ContentType: "application/octet-stream", SizeBytes: 1,
		})
		require.NoError(t, err)
		return id
	}
	a1 := mkAtt(m1, "1.1", "a.txt")
	a2 := mkAtt(m1, "1.2", "b.txt")
	a3 := mkAtt(m3, "3.1", "c.txt")

	got, err := s.ListAttachmentsByMessages(ctx, []int64{m1, m2, m3, 9999})
	require.NoError(t, err)

	require.Len(t, got, 2, "m2 and 9999 should be absent from map")
	require.Len(t, got[m1], 2)
	require.Equal(t, a1, got[m1][0].ID)
	require.Equal(t, a2, got[m1][1].ID)
	require.Len(t, got[m3], 1)
	require.Equal(t, a3, got[m3][0].ID)
	_, m2Present := got[m2]
	require.False(t, m2Present, "messages without attachments must be absent from map")
}

func TestListAttachmentsByMessages_EmptyInput(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListAttachmentsByMessages(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = s.ListAttachmentsByMessages(context.Background(), []int64{})
	require.NoError(t, err)
	require.Empty(t, got)
}
