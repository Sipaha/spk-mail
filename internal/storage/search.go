package storage

import (
	"context"
	"fmt"
	"strings"
)

// Snippet sentinels — chosen to never appear in real text. The frontend
// splits on these to render highlights as React elements (no innerHTML).
const (
	SnippetBegin = "\x01"
	SnippetEnd   = "\x02"
)

type SearchHit struct {
	MessageID int64
	ThreadID  *int64
	Subject   string
	FromAddr  string
	Date      int64
	Snippet   string // contains \x01 ... \x02 sentinels around matches
}

func (s *Store) Search(ctx context.Context, query string, limit, offset int) ([]SearchHit, error) {
	spec := ParseSearchQuery(query)
	if limit <= 0 {
		limit = 100
	}

	var (
		joinFTS  bool
		whereSQL []string
		args     []any
	)
	selectExpr := `m.id, m.thread_id, m.subject, m.from_addr, m.date, ''`

	if spec.MatchExpr != "" {
		joinFTS = true
		selectExpr = `m.id, m.thread_id, m.subject, m.from_addr, m.date, snippet(messages_fts, 3, '` + SnippetBegin + `', '` + SnippetEnd + `', '…', 16)`
		whereSQL = append(whereSQL, "messages_fts MATCH ?")
		args = append(args, spec.MatchExpr)
	}
	for _, f := range spec.From {
		whereSQL = append(whereSQL, "m.from_addr LIKE ?")
		args = append(args, "%"+f+"%")
	}
	for _, t := range spec.To {
		whereSQL = append(whereSQL, "m.to_addrs  LIKE ?")
		args = append(args, "%"+t+"%")
	}
	for _, sj := range spec.Subject {
		whereSQL = append(whereSQL, "m.subject  LIKE ?")
		args = append(args, "%"+sj+"%")
	}
	if spec.HasAttachment {
		whereSQL = append(whereSQL, "m.has_attachments = 1")
	}
	if spec.UnreadOnly {
		whereSQL = append(whereSQL, "m.flags NOT LIKE '%\\Seen%'")
	}
	if len(spec.AccountIDs) > 0 {
		ph := strings.Repeat("?,", len(spec.AccountIDs))
		ph = ph[:len(ph)-1]
		whereSQL = append(whereSQL, "m.account_id IN ("+ph+")")
		for _, id := range spec.AccountIDs {
			args = append(args, id)
		}
	}
	if len(whereSQL) == 0 {
		return nil, nil // no filter ⇒ no results
	}

	q := "SELECT " + selectExpr + " FROM messages m"
	if joinFTS {
		q += " JOIN messages_fts ON messages_fts.rowid = m.id"
	}
	q += " WHERE " + strings.Join(whereSQL, " AND ")
	q += " ORDER BY m.date DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var subj, from *string
		if err := rows.Scan(&h.MessageID, &h.ThreadID, &subj, &from, &h.Date, &h.Snippet); err != nil {
			return nil, err
		}
		if subj != nil {
			h.Subject = *subj
		}
		if from != nil {
			h.FromAddr = *from
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
