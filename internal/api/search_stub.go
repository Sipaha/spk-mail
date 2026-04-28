package api

import "context"

func (s *Stub) Search(ctx context.Context, query string, limit, offset int) ([]SearchHitDTO, error) {
	hits, err := s.Store.Search(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHitDTO{
			MessageID: h.MessageID,
			ThreadID:  h.ThreadID,
			Subject:   h.Subject,
			FromAddr:  h.FromAddr,
			Date:      h.Date,
			Snippet:   h.Snippet,
		})
	}
	return out, nil
}
