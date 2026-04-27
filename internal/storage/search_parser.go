package storage

import (
	"strconv"
	"strings"
)

type SearchSpec struct {
	MatchExpr     string   // FTS5 MATCH expression, empty if none
	From          []string // from: filters (substring on from_addr)
	To            []string // to:
	Subject       []string // subject:
	HasAttachment bool
	UnreadOnly    bool
	AccountIDs    []int64
}

// ParseSearchQuery tokenizes the input. Tokens of the form `key:value` are
// recognized as operators; everything else becomes an FTS MATCH term.
// Quoted "phrases" preserve spaces.
func ParseSearchQuery(q string) SearchSpec {
	var s SearchSpec
	tokens := tokenize(q)
	var matchTerms []string
	for _, t := range tokens {
		if i := strings.IndexByte(t, ':'); i > 0 && !strings.HasPrefix(t, `"`) {
			key, val := strings.ToLower(t[:i]), t[i+1:]
			val = strings.Trim(val, `"`)
			switch key {
			case "from":
				s.From = append(s.From, val)
			case "to":
				s.To = append(s.To, val)
			case "subject":
				s.Subject = append(s.Subject, val)
			case "has":
				if strings.EqualFold(val, "attachment") || strings.EqualFold(val, "attachments") {
					s.HasAttachment = true
				}
			case "account":
				if id, err := strconv.ParseInt(val, 10, 64); err == nil {
					s.AccountIDs = append(s.AccountIDs, id)
				}
			default:
				matchTerms = append(matchTerms, ftsQuote(t))
			}
			continue
		}
		if strings.EqualFold(t, "unread") {
			s.UnreadOnly = true
			continue
		}
		matchTerms = append(matchTerms, ftsQuote(t))
	}
	s.MatchExpr = strings.TrimSpace(strings.Join(matchTerms, " "))
	return s
}

func ftsQuote(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	if strings.HasPrefix(t, `"`) && strings.HasSuffix(t, `"`) && len(t) > 1 {
		return t
	}
	return `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
}

// tokenize splits on whitespace, preserving "quoted phrases".
func tokenize(q string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			cur.WriteRune(r)
			inQ = !inQ
		case r == ' ' && !inQ:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}
