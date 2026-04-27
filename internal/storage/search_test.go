package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedSearchData(t *testing.T) (*Store, int64) {
	t.Helper()
	s := openTestStore(t)
	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, AccountRow{Name: "X", Email: "a@x", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0})
	role := "inbox"
	fID, _ := s.UpsertFolder(ctx, FolderRow{AccountID: accID, Name: "INBOX", Delimiter: "/", Role: &role, UIDValidity: 1, UIDNext: 1})

	insert := func(uid int64, subj, from, body string, hasAtt bool, seen bool) int64 {
		flags := "[]"
		if seen {
			flags = `["\\Seen"]`
		}
		id, err := s.InsertMessage(ctx, MessageRow{
			AccountID: accID, FolderID: fID, UID: uid, Date: uid,
			Subject: stringPtr(subj), FromAddr: stringPtr(from),
			BodyText: stringPtr(body), Flags: flags, HasAttachments: hasAtt,
		})
		require.NoError(t, err)
		return id
	}
	insert(1, "Project update Q2", "Bob <bob@x.y>", "the latest milestones", false, false)
	insert(2, "Re: Project update Q2", "Alice <a@x.y>", "thanks for the update", true, true)
	insert(3, "Newsletter weekly", "news@x.y", "stories of the week", false, false)
	insert(4, "Invoice 1234", "billing@x.y", "please pay invoice attached", true, false)
	return s, accID
}

func TestSearch_FreeText(t *testing.T) {
	s, _ := seedSearchData(t)
	hits, err := s.Search(context.Background(), "milestones", 50, 0)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Snippet, "milestones")
	// Verify sentinel markers wrap the matched word
	require.Contains(t, hits[0].Snippet, SnippetBegin+"milestones"+SnippetEnd)
}

func TestSearch_FromOperator(t *testing.T) {
	s, _ := seedSearchData(t)
	hits, _ := s.Search(context.Background(), "from:bob", 50, 0)
	require.Len(t, hits, 1)
}

func TestSearch_HasAttachment(t *testing.T) {
	s, _ := seedSearchData(t)
	hits, _ := s.Search(context.Background(), "has:attachment", 50, 0)
	require.Len(t, hits, 2)
}

func TestSearch_Unread(t *testing.T) {
	s, _ := seedSearchData(t)
	hits, _ := s.Search(context.Background(), "unread", 50, 0)
	require.GreaterOrEqual(t, len(hits), 3)
	for _, h := range hits {
		var flags string
		require.NoError(t, s.DB().QueryRow(`SELECT flags FROM messages WHERE id = ?`, h.MessageID).Scan(&flags))
		require.False(t, strings.Contains(flags, `\Seen`))
	}
}

func TestSearch_FreeTextPlusOperator(t *testing.T) {
	s, _ := seedSearchData(t)
	hits, _ := s.Search(context.Background(), "from:alice update", 50, 0)
	require.Len(t, hits, 1)
}
