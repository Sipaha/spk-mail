package testapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
)

type seedHandler struct {
	api   api.API
	mock  *mockimap.Server
	clock *clock.Clock
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
	// Also write accounts to the DB so the UI shows them immediately.
	for _, acc := range f.Accounts {
		host, port := mockHostPort(h.mock)
		pw := acc.Password
		if pw == "" {
			pw = "secret"
		}
		_, err := h.api.AddAccount(r.Context(), api.AddAccountRequest{
			Name:         acc.Name,
			Email:        acc.Email,
			IMAPHost:     host,
			IMAPPort:     port,
			IMAPUsername: acc.Email,
			IMAPPassword: pw,
			UseTLS:       false,
			Color:        acc.Color,
			UseMock:      true,
		})
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
