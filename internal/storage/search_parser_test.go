package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseQuery_FreeText(t *testing.T) {
	s := ParseSearchQuery("hello world")
	require.Equal(t, `"hello" "world"`, s.MatchExpr)
	require.Empty(t, s.From)
	require.False(t, s.UnreadOnly)
}

func TestParseQuery_Operators(t *testing.T) {
	s := ParseSearchQuery("from:bob subject:invoice has:attachment unread account:1 hello")
	require.Equal(t, `"hello"`, s.MatchExpr)
	require.Equal(t, []string{"bob"}, s.From)
	require.Equal(t, []string{"invoice"}, s.Subject)
	require.True(t, s.HasAttachment)
	require.True(t, s.UnreadOnly)
	require.Equal(t, []int64{1}, s.AccountIDs)
}

func TestParseQuery_QuotedPhrase(t *testing.T) {
	s := ParseSearchQuery(`"weekly report" project`)
	require.Equal(t, `"weekly report" "project"`, s.MatchExpr)
}

func TestParseQuery_EmptyMatchAllowed(t *testing.T) {
	// Operators only — MatchExpr empty triggers a non-FTS path
	s := ParseSearchQuery("unread")
	require.Empty(t, s.MatchExpr)
	require.True(t, s.UnreadOnly)
}
