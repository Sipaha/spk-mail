package storage

// Search is implemented in plan 5. Stub returns empty results so the API surface
// can be wired up earlier without depending on FTS5 query parsing.
import "context"

type SearchHit struct {
	MessageID int64
	Snippet   string
}

func (s *Store) Search(ctx context.Context, query string, limit, offset int) ([]SearchHit, error) {
	return nil, nil
}
