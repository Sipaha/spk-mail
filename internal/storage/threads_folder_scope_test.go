package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Reproduces the bug where replying to an Inbox thread puts the Sent message's
// date, sender, and snippet at the top of the Inbox folder view.
func TestListThreads_FolderScopeDoesNotLeakSentMetadataIntoInbox(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "Alice", Email: "alice@example.com",
		IMAPHost: "127.0.0.1", IMAPPort: 143, IMAPUsername: "alice@example.com",
		UseTLS: false, Color: "#3b82f6", CreatedAt: 0,
	})
	require.NoError(t, err)

	inboxRole := "inbox"
	sentRole := "sent"
	inboxID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &inboxRole, UIDValidity: 1, UIDNext: 10,
	})
	require.NoError(t, err)
	sentID, err := s.UpsertFolder(ctx, FolderRow{
		AccountID: accID, Name: "Sent", Delimiter: "/", Role: &sentRole, UIDValidity: 1, UIDNext: 10,
	})
	require.NoError(t, err)

	// Thread 1: incoming message in INBOX at t=100, then user sent a reply in Sent at t=500.
	_, tid, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "ticket 42", LastDate: 100},
		Message: MessageRow{
			AccountID: accID, FolderID: inboxID, UID: 1,
			MessageID: stringPtr("<inc-1@example.test>"),
			Subject:   stringPtr("Ticket 42"),
			FromAddr:  stringPtr("Support Desk <support@example.test>"),
			Date:      100,
			Flags:     `["\\Seen"]`,
			BodyText:  stringPtr("Incoming support message"),
		},
	})
	require.NoError(t, err)

	_, _, err = s.InsertParsedMessageBundle(ctx, MessageBundle{
		ExistingThreadID: tid,
		Message: MessageRow{
			AccountID: accID, FolderID: sentID, UID: 1,
			MessageID: stringPtr("<sent-1@example.com>"),
			InReplyTo: stringPtr("<inc-1@example.test>"),
			Subject:   stringPtr("Re: Ticket 42"),
			FromAddr:  stringPtr("Alice <alice@example.com>"),
			Date:      500,
			Flags:     `["\\Seen"]`,
			BodyText:  stringPtr("Thanks, certificates fixed!"),
		},
	})
	require.NoError(t, err)

	// Thread 2: standalone incoming message in INBOX at t=300 (newer than Thread 1's Inbox message!).
	_, tid2, err := s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "newer inbox mail", LastDate: 300},
		Message: MessageRow{
			AccountID: accID, FolderID: inboxID, UID: 2,
			MessageID: stringPtr("<inc-2@example.test>"),
			Subject:   stringPtr("Newer inbox mail"),
			FromAddr:  stringPtr("Bot <bot@example.test>"),
			Date:      300,
			Flags:     `[]`,
			BodyText:  stringPtr("Fresh notification"),
		},
	})
	require.NoError(t, err)

	// Thread 3: pure Sent message (must never appear in Inbox).
	_, _, err = s.InsertParsedMessageBundle(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "outbound only", LastDate: 600},
		Message: MessageRow{
			AccountID: accID, FolderID: sentID, UID: 2,
			MessageID: stringPtr("<sent-only@example.com>"),
			Subject:   stringPtr("Outbound only"),
			FromAddr:  stringPtr("Alice <alice@example.com>"),
			Date:      600,
			Flags:     `["\\Seen"]`,
			BodyText:  stringPtr("Outbound message"),
		},
	})
	require.NoError(t, err)

	// Inbox folder view must reflect ONLY messages in INBOX:
	// - Ordered by INBOX date: Thread 2 (t=300) BEFORE Thread 1 (t=100).
	// - Thread 1 must show Support Desk / t=100 / MsgCount=1 / incoming snippet,
	//   NOT Alice / t=500 / "Thanks, certificates fixed!".
	inboxThreads, err := s.ListThreads(ctx, ThreadFilter{AccountID: &accID, FolderID: &inboxID}, 50, 0)
	require.NoError(t, err)
	require.Len(t, inboxThreads, 2)
	require.Equal(t, tid2, inboxThreads[0].ID, "newer INBOX mail (t=300) must sort ahead of older INBOX mail (t=100)")
	require.Equal(t, tid, inboxThreads[1].ID)
	require.Equal(t, int64(100), inboxThreads[1].LastDate)
	require.Equal(t, int64(1), inboxThreads[1].MsgCount)
	require.NotNil(t, inboxThreads[1].LastFrom)
	require.Equal(t, "Support Desk <support@example.test>", *inboxThreads[1].LastFrom)
	require.NotNil(t, inboxThreads[1].Snippet)
	require.Equal(t, "Incoming support message", *inboxThreads[1].Snippet)

	// Sent folder view must reflect ONLY messages in Sent.
	sentThreads, err := s.ListThreads(ctx, ThreadFilter{AccountID: &accID, FolderID: &sentID}, 50, 0)
	require.NoError(t, err)
	require.Len(t, sentThreads, 2)
	require.Equal(t, "Alice <alice@example.com>", *sentThreads[1].LastFrom)
	require.Equal(t, int64(500), sentThreads[1].LastDate)
	require.Equal(t, int64(1), sentThreads[1].MsgCount)

	// Unscoped All mail view must preserve the full conversation (t=500, MsgCount=2).
	allThreads, err := s.ListThreads(ctx, ThreadFilter{}, 50, 0)
	require.NoError(t, err)
	require.Len(t, allThreads, 3)
	var found *ThreadRow
	for i := range allThreads {
		if allThreads[i].ID == tid {
			found = &allThreads[i]
			break
		}
	}
	require.NotNil(t, found)
	require.Equal(t, int64(500), found.LastDate)
	require.Equal(t, int64(2), found.MsgCount)
}

func TestListThreads_FolderScopeUnreadFlaggedAndScale(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	accID, err := s.InsertAccount(ctx, AccountRow{
		Name: "Alice", Email: "alice@example.com",
		IMAPHost: "127.0.0.1", IMAPPort: 143, IMAPUsername: "alice@example.com",
		UseTLS: false, Color: "#3b82f6",
	})
	require.NoError(t, err)
	inboxRole, sentRole := "inbox", "sent"
	inboxID, err := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &inboxRole, UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)
	sentID, err := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "Sent", Delimiter: "/", Role: &sentRole, UIDValidity: 1, UIDNext: 1})
	require.NoError(t, err)

	// Read INBOX message + unread Sent message in the same thread:
	// Inbox+UnreadOnly must NOT return it; Inbox+HasFlagged must NOT return it.
	_, tid, inserted, err := s.InsertParsedMessageBundleIfNew(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "mixed flags", LastDate: 100},
		Message:   MessageRow{AccountID: accID, FolderID: inboxID, UID: 1, Date: 100, Flags: `["\\Seen"]`, BodyText: stringPtr("in")},
	})
	require.NoError(t, err)
	require.True(t, inserted)
	_, _, inserted, err = s.InsertParsedMessageBundleIfNew(ctx, MessageBundle{
		ExistingThreadID: tid,
		Message:          MessageRow{AccountID: accID, FolderID: sentID, UID: 1, Date: 200, Flags: `["\\Flagged"]`, BodyText: stringPtr("out")},
	})
	require.NoError(t, err)
	require.True(t, inserted)

	// Re-inserting an already-present UID must deduplicate without creating an orphan thread.
	msgID2, tid2, inserted, err := s.InsertParsedMessageBundleIfNew(ctx, MessageBundle{
		NewThread: ThreadRow{SubjectNorm: "orphan candidate", LastDate: 999},
		Message:   MessageRow{AccountID: accID, FolderID: inboxID, UID: 1, Date: 100, Flags: `["\\Seen"]`},
	})
	require.NoError(t, err)
	require.False(t, inserted)
	require.Greater(t, msgID2, int64(0))
	require.Equal(t, tid, tid2)

	unreadInbox, err := s.ListThreads(ctx, ThreadFilter{FolderID: &inboxID, UnreadOnly: true}, 50, 0)
	require.NoError(t, err)
	require.Empty(t, unreadInbox)

	flaggedInbox, err := s.ListThreads(ctx, ThreadFilter{FolderID: &inboxID, HasFlagged: true}, 50, 0)
	require.NoError(t, err)
	require.Empty(t, flaggedInbox)

	// Bulk scale test: 3,000 messages in INBOX must query fast via V11 covering indexes.
	require.NoError(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		for i := 2; i <= 3001; i++ {
			tID, err := insertThread(ctx, tx, ThreadRow{SubjectNorm: fmt.Sprintf("bulk-%d", i), LastDate: int64(1000 + i), MsgCount: 1})
			if err != nil {
				return err
			}
			_, err = insertMessage(ctx, tx, MessageRow{
				AccountID: accID, FolderID: inboxID, UID: int64(i),
				ThreadID: &tID, Subject: stringPtr("bulk"), FromAddr: stringPtr("s@example.test"),
				Date: int64(1000 + i), Flags: `["\\Seen"]`, BodyText: stringPtr("body"),
			})
			if err != nil {
				return err
			}
		}
		return nil
	}))

	start := time.Now()
	page, err := s.ListThreads(ctx, ThreadFilter{AccountID: &accID, FolderID: &inboxID}, 200, 0)
	require.NoError(t, err)
	require.Len(t, page, 200)
	require.Less(t, time.Since(start), 250*time.Millisecond)
	require.Equal(t, int64(4001), page[0].LastDate)
}
