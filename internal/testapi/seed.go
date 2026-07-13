package testapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

type seedHandler struct {
	api   api.API
	mock  *mockimap.Server
	clock *clock.Clock
	store *storage.Store
}

func (h *seedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTestAPIBodyBytes)
	var f mockimap.Fixture
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, "expected JSON fixture body", http.StatusBadRequest)
		return
	}
	applier := &fixtureApplier{api: h.api, mock: h.mock, clock: h.clock, store: h.store}
	if err := applier.apply(r.Context(), &f); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func mockHostPort(s *mockimap.Server) (string, int) {
	if s == nil {
		return "", 0
	}
	host, port, _ := strings.Cut(s.Addr(), ":")
	if !portValid(port) {
		return host, 0
	}
	p, _ := strconv.Atoi(port)
	return host, p
}

func portValid(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}
