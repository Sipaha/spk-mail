package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrettyFolderName(t *testing.T) {
	cases := []struct {
		in    string
		delim string
		want  string
	}{
		// All-caps single segment → Title Case.
		{"INBOX", "/", "Inbox"},
		{"GMAIL", "/", "Gmail"},
		// Already mixed case → passthrough (no destructive Title Case).
		{"Sent", "/", "Sent"},
		{"Spam folder", "/", "Spam folder"},
		{"All Mail", "/", "All Mail"},
		// Hierarchy: each segment is normalized independently.
		{"Drafts|template", "|", "Drafts|template"},
		{"INBOX|TEMPLATE", "|", "Inbox|Template"},
		// Non-ASCII must NOT be mangled (we only handle plain ASCII upper-case).
		{"мусор", "/", "мусор"},
		// Empty / no-delimiter edge cases.
		{"", "/", ""},
		{"INBOX", "", "Inbox"},
	}
	for _, tc := range cases {
		got := prettyFolderName(tc.in, tc.delim)
		require.Equal(t, tc.want, got, "input=%q delim=%q", tc.in, tc.delim)
	}
}
