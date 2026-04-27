package testapi

import (
	"encoding/json"
	"net/http"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/storage"
)

func dbDumpHandler(a api.API, s *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		out := map[string]any{}
		accs, _ := a.ListAccounts(ctx)
		out["accounts"] = accs
		threads, _ := s.ListThreadsRecent(ctx, 1000, 0)
		out["threads"] = threads
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
