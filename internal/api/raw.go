package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrRawUnavailable signals that raw bytes can't be obtained — the
// message's IMAP UID is no longer present on the server (deleted from
// another client, or moved to a folder we don't track). Distinct from
// generic transport errors so the frontend can render a focused
// message instead of "internal error".
var ErrRawUnavailable = errors.New("raw RFC822 unavailable for this message")

// rawFilename builds the .eml filename used for download. Priority:
//
//  1. subject sanitized + truncated to 80 runes
//  2. message-id (without surrounding < >) sanitized
//  3. "message-<msgID>.eml" as a last resort
//
// Sanitization replaces filesystem-hostile characters with "_", trims
// trailing dots and whitespace, and caps at 80 runes (UTF-8 aware).
func rawFilename(subject *string, messageID *string, msgID int64) string {
	if name := cleanCandidate(derefSafe(subject)); name != "" {
		return name + ".eml"
	}
	if mid := derefSafe(messageID); mid != "" {
		mid = strings.TrimPrefix(mid, "<")
		mid = strings.TrimSuffix(mid, ">")
		if name := cleanCandidate(mid); name != "" {
			return name + ".eml"
		}
	}
	return fmt.Sprintf("message-%d.eml", msgID)
}

func derefSafe(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var fnameSanitizer = regexp.MustCompile(`[\\/<>:"|?*\x00-\x1f]`)

func cleanCandidate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = fnameSanitizer.ReplaceAllString(s, "_")
	s = strings.TrimRight(s, ". ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}
