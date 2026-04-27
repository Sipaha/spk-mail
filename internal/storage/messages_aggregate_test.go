package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateBodyHTML(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	id, _ := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: fID, UID: 1, Date: 0, Flags: "[]",
		BodyHTML: stringPtr("<p>blocked</p>"),
	})

	require.NoError(t, s.UpdateBodyHTML(ctx, id, "<p>allowed</p>"))

	got, err := s.GetMessage(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.BodyHTML)
	require.Equal(t, "<p>allowed</p>", *got.BodyHTML)
}

func TestUnreadCountsByAccount_Empty(t *testing.T) {
	s := openTestStore(t)
	total, per, err := s.UnreadCountsByAccount(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), total)
	require.Empty(t, per)
}

func TestUnreadCountsByAccount_WithData(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	a1, _ := s.InsertAccount(ctx, AccountRow{Name: "A1", Email: "a1@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	a2, _ := s.InsertAccount(ctx, AccountRow{Name: "A2", Email: "a2@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	inboxRole := "inbox"
	otherRole := "sent"
	fInbox1, _ := s.UpsertFolder(ctx, FolderRow{AccountID: a1, Name: "INBOX", Delimiter: "/", Role: &inboxRole, UIDValidity: 1, UIDNext: 1})
	fInbox2, _ := s.UpsertFolder(ctx, FolderRow{AccountID: a2, Name: "INBOX", Delimiter: "/", Role: &inboxRole, UIDValidity: 1, UIDNext: 1})
	fSent1, _ := s.UpsertFolder(ctx, FolderRow{AccountID: a1, Name: "Sent", Delimiter: "/", Role: &otherRole, UIDValidity: 1, UIDNext: 1})

	seen, _ := json.Marshal([]string{`\Seen`})
	empty, _ := json.Marshal([]string{})
	// Substring-decoy: a flag value that contains "Seen" as substring should
	// NOT count as seen — proves we use json_each, not LIKE.
	decoy, _ := json.Marshal([]string{`Seenmaybe`})

	// a1 inbox: 1 unread, 1 seen, 1 decoy-unread -> total 2
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: a1, FolderID: fInbox1, UID: 1, Date: 1, Flags: string(empty)})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: a1, FolderID: fInbox1, UID: 2, Date: 2, Flags: string(seen)})
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: a1, FolderID: fInbox1, UID: 3, Date: 3, Flags: string(decoy)})
	// a1 sent: 1 unread (should NOT count — not inbox)
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: a1, FolderID: fSent1, UID: 1, Date: 1, Flags: string(empty)})
	// a2 inbox: 1 unread
	_, _ = s.InsertMessage(ctx, MessageRow{AccountID: a2, FolderID: fInbox2, UID: 1, Date: 1, Flags: string(empty)})

	total, per, err := s.UnreadCountsByAccount(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Equal(t, int64(2), per[a1])
	require.Equal(t, int64(1), per[a2])
}
