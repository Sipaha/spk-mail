package storage

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMessages_InsertAndList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	folderID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})

	id, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1,
		MessageID: stringPtr("<a@x>"), Subject: stringPtr("Hello"),
		FromAddr: stringPtr("Bob <b@x.y>"), Date: 1700000000,
		Flags:    `[]`,
		BodyText: stringPtr("hi"),
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := s.GetMessage(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Hello", *got.Subject)
}

// TestFindThreadByMessageIDs_MatchesInReplyTo verifies that the primary
// reference-chain thread lookup attaches a reply to the same thread bucket
// as the parent. This is the path the StoreWriter takes on every message
// insert, so a regression silently splits conversations into singletons.
func TestFindThreadByMessageIDs_MatchesInReplyTo(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "x@y.z", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff"})
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1})
	threadID, _ := s.InsertThread(ctx, ThreadRow{SubjectNorm: "topic", LastDate: 1700000000, MsgCount: 1})

	// Parent message lives in the thread.
	_, err := s.InsertMessage(ctx, MessageRow{
		AccountID: accID, FolderID: fID, UID: 1, ThreadID: &threadID,
		MessageID: stringPtr("<parent@x>"), Subject: stringPtr("topic"),
		Date: 1700000000, Flags: `[]`,
	})
	require.NoError(t, err)

	// FindThreadByMessageIDs must return the thread for the parent's id.
	id, ok, err := s.FindThreadByMessageIDs(ctx, []string{"<parent@x>"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, threadID, id)

	// And miss for an id we have not inserted.
	_, ok, err = s.FindThreadByMessageIDs(ctx, []string{"<unknown@x>"})
	require.NoError(t, err)
	require.False(t, ok)

	// Empty input returns ok=false without an error.
	_, ok, err = s.FindThreadByMessageIDs(ctx, nil)
	require.NoError(t, err)
	require.False(t, ok)

	// Multi-id input — production usage passes the full References chain
	// (typically 3-5 ids) and expects to get back the thread of any one
	// that matches. Mix one hit with two misses and assert the parent's
	// thread id is what comes back.
	id, ok, err = s.FindThreadByMessageIDs(ctx,
		[]string{"<unknown1@x>", "<parent@x>", "<unknown2@x>"})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, threadID, id)
}
