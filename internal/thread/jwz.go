// Package thread implements client-side conversation grouping (RFC 5322
// In-Reply-To / References headers) and Re:/Fwd: subject normalization.
package thread

import (
	"strings"
	"unicode"
)

// CandidateMessageIDs returns the set of message-ids a new message could be
// "threaded under". This is everything in References plus In-Reply-To,
// deduplicated and trimmed.
func CandidateMessageIDs(inReplyTo string, references []string) []string {
	seen := map[string]struct{}{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
	}
	for _, r := range references {
		add(r)
	}
	add(inReplyTo)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

var prefixWords = map[string]struct{}{
	"re": {}, "fwd": {}, "fw": {}, "antw": {}, "aw": {}, "rv": {}, "tr": {},
}

// NormalizeSubject strips repeated "Re:" / "Fwd:" prefixes and surrounding
// whitespace, and lowercases. Used to fall-back-thread messages that share
// a topic but have no References header (mailing lists that strip headers).
func NormalizeSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		i := 0
		for i < len(s) && (unicode.IsLetter(rune(s[i]))) {
			i++
		}
		if i == 0 || i >= len(s) || s[i] != ':' {
			break
		}
		word := strings.ToLower(s[:i])
		if _, ok := prefixWords[word]; !ok {
			break
		}
		s = strings.TrimSpace(s[i+1:])
	}
	return strings.ToLower(s)
}
