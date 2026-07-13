package api

import (
	"context"
	"testing"

	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestListFolders_OrderedByRole(t *testing.T) {
	a := testStub(t)
	ctx := context.Background()

	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{
		Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	inbox, sent, drafts, custom, spam := "inbox", "sent", "drafts", "", "spam"
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Custom", Delimiter: "/", Role: &custom, UIDValidity: 1, UIDNext: 1})
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Trash", Delimiter: "/", UIDValidity: 1, UIDNext: 1}) // role nil → ""
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &inbox, UIDValidity: 1, UIDNext: 1})
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Sent", Delimiter: "/", Role: &sent, UIDValidity: 1, UIDNext: 1})
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Drafts", Delimiter: "/", Role: &drafts, UIDValidity: 1, UIDNext: 1})
	_, _ = a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Spam folder", Delimiter: "/", Role: &spam, UIDValidity: 1, UIDNext: 1})

	fs, err := a.ListFolders(ctx, accID)
	require.NoError(t, err)
	require.Len(t, fs, 6)

	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Name
	}
	// Expected sort: inbox(0), sent(1), drafts(2), archive(3-none), ""(4) [Custom, Trash alphabetical], spam(5)
	// Display names: ALL-CAPS segments are title-cased ("INBOX" → "Inbox");
	// already-mixed-case names ("Sent", "Drafts", …) pass through untouched.
	require.Equal(t, []string{"Inbox", "Sent", "Drafts", "Custom", "Trash", "Spam folder"}, names)
}

func TestListFolders_UnreadCounts(t *testing.T) {
	a := testStub(t)
	ctx := context.Background()
	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	inbox := "inbox"
	fid, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &inbox, UIDValidity: 1, UIDNext: 1})

	_, _ = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fid, UID: 1, Date: 1, Flags: `[]`})         // unread
	_, _ = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fid, UID: 2, Date: 2, Flags: `["\\Seen"]`}) // read
	_, _ = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fid, UID: 3, Date: 3, Flags: `[]`})         // unread

	fs, err := a.ListFolders(ctx, accID)
	require.NoError(t, err)
	require.Len(t, fs, 1)
	require.Equal(t, "Inbox", fs[0].Name)
	require.Equal(t, int64(2), fs[0].UnreadCount)
	require.Equal(t, int64(3), fs[0].TotalCount)
	require.Equal(t, int64(0), fs[0].FlaggedCount)
}

func TestListThreads_HonorsFolderAndUnreadFilters(t *testing.T) {
	a := testStub(t)
	ctx := context.Background()
	accID, _ := a.Store.InsertAccount(ctx, storage.AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	inbox, sent := "inbox", "sent"
	fInbox, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &inbox, UIDValidity: 1, UIDNext: 1})
	fSent, _ := a.Store.UpsertFolder(ctx, storage.FolderRow{AccountID: accID, Name: "Sent", Delimiter: "/", Role: &sent, UIDValidity: 1, UIDNext: 1})

	// InsertThread is test scaffolding not on the Writer interface; assert to concrete type.
	concreteStore := a.Store.(*storage.Store)
	tInbox, _ := concreteStore.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "in", LastDate: 100, MsgCount: 1, UnreadCount: 1})
	tSent, _ := concreteStore.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "sent", LastDate: 200, MsgCount: 1, UnreadCount: 0})

	_, _ = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fInbox, UID: 1, ThreadID: &tInbox, Date: 100, Flags: `[]`})
	_, _ = a.Store.InsertMessage(ctx, storage.MessageRow{AccountID: accID, FolderID: fSent, UID: 1, ThreadID: &tSent, Date: 200, Flags: `["\\Seen"]`})

	inboxOnly, err := a.ListThreads(ctx, ThreadFilter{FolderID: &fInbox})
	require.NoError(t, err)
	require.Len(t, inboxOnly, 1)
	require.Equal(t, "in", inboxOnly[0].Subject)

	unreadOnly, err := a.ListThreads(ctx, ThreadFilter{UnreadOnly: true})
	require.NoError(t, err)
	require.Len(t, unreadOnly, 1)
	require.Equal(t, "in", unreadOnly[0].Subject)
}
