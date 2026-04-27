package testapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/spk/spk-mail/internal/clock"
)

type clockReq struct {
	Now   string `json:"now,omitempty"`
	Reset bool   `json:"reset,omitempty"`
}

func clockHandler(c *clock.Clock) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req clockReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Reset {
			c.Reset()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		t, err := time.Parse(time.RFC3339, req.Now)
		if err != nil {
			http.Error(w, "invalid 'now': "+err.Error(), http.StatusBadRequest)
			return
		}
		c.Set(t)
		w.WriteHeader(http.StatusNoContent)
	}
}
