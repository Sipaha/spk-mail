package thread

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSubject(t *testing.T) {
	for _, tc := range []struct{ in, out string }{
		{"Re: hello", "hello"},
		{"RE: re: Fwd: hi", "hi"},
		{"  Re:  spaced  ", "spaced"},
		{"Hello", "hello"},
		{"Re:", ""},
	} {
		require.Equal(t, tc.out, NormalizeSubject(tc.in), tc.in)
	}
}

func TestCandidateMessageIDs(t *testing.T) {
	got := CandidateMessageIDs("<r@x>", []string{"<a@x>", "<b@x>"})
	require.ElementsMatch(t, []string{"<a@x>", "<b@x>", "<r@x>"}, got)

	got = CandidateMessageIDs("", nil)
	require.Empty(t, got)
}
