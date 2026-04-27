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
	var f mockimap.Fixture
	if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
		http.Error(w, "expected JSON fixture body", http.StatusBadRequest)
		return
	}
	if h.mock != nil {
		var ns mockimap.NowSource
		if h.clock != nil {
			ns = h.clock
		}
		if err := h.mock.ApplyWithClock(&f, ns); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Resolve a default profile so seeded accounts attach to the same
	// profile the UI is currently showing. Without this, accounts come back
	// with profile_id NULL and the per-profile sidebar filters them out.
	var defaultProfileID int64
	if h.store != nil {
		if profiles, err := h.store.ListProfiles(r.Context()); err == nil && len(profiles) > 0 {
			defaultProfileID = profiles[0].ID
		}
	}
	// Also write accounts to the DB so the UI shows them immediately.
	for _, acc := range f.Accounts {
		host, port := mockHostPort(h.mock)
		pw := acc.Password
		if pw == "" {
			pw = "secret"
		}
		req := api.AddAccountRequest{
			Name:         acc.Name,
			Email:        acc.Email,
			IMAPHost:     host,
			IMAPPort:     port,
			IMAPUsername: acc.Email,
			IMAPPassword: pw,
			UseTLS:       false,
			Color:        acc.Color,
			UseMock:      true,
		}
		if defaultProfileID > 0 {
			pid := defaultProfileID
			req.ProfileID = &pid
		}
		_, err := h.api.AddAccount(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
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
