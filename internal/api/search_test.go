package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestStub_Search_ReturnsHitsWithSentinelSnippet(t *testing.T) {
	a := testStub(t)
	st := a.Store
	ctx := context.Background()

	accountID, err := st.InsertAccount(ctx, storage.AccountRow{
		Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993, IMAPUsername: "u",
		UseTLS: true, Color: "#fff", CreatedAt: time.Now().Unix(),
	})
	require.NoError(t, err)

	inboxRole := "inbox"
	folderID, err := st.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accountID, Name: "INBOX", Delimiter: "/", Role: &inboxRole,
		UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	subj := "Greetings"
	from := "Bob <bob@x.y>"
	body := "hello world meaningful body"
	_, err = st.InsertMessage(ctx, storage.MessageRow{
		AccountID: accountID, FolderID: folderID, UID: 1,
		Date: time.Now().Unix(), Flags: "[]",
		Subject: &subj, FromAddr: &from, BodyText: &body,
	})
	require.NoError(t, err)

	hits, err := a.Search(ctx, "meaningful", 50, 0)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	h := hits[0]
	require.Equal(t, subj, h.Subject)
	require.Equal(t, from, h.FromAddr)
	require.Contains(t, h.Snippet, "meaningful")
	require.True(t, strings.Contains(h.Snippet, storage.SnippetBegin+"meaningful"+storage.SnippetEnd),
		"expected sentinel-wrapped match in snippet, got %q", h.Snippet)
}

func TestStub_Search_NoMatch(t *testing.T) {
	a := testStub(t)
	hits, err := a.Search(context.Background(), "nothingmatches", 50, 0)
	require.NoError(t, err)
	require.Empty(t, hits)
}
