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
		// RFC 2047 encoded-words: by the time we run NormalizeSubject the
		// upstream MIME parser has already decoded these via decodeHeader,
		// so encoded-form input is the corner case where decoding failed
		// or the field never went through it. Document the graceful
		// pass-through so a later refactor that adds prefix-detection
		// over the encoded form doesn't surprise itself.
		{"=?utf-8?B?aGk=?= Re: topic", "=?utf-8?b?agk=?= re: topic"},
		{"Re: =?utf-8?Q?hello?=", "=?utf-8?q?hello?="},
		// Non-English Re: prefixes outside the prefixWords set should not
		// be stripped; lowercasing is the only mutation.
		{"Отв: hello", "отв: hello"},
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
