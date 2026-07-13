package testapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

type resetHandler struct {
	api            api.API
	mock           *mockimap.Server
	clock          *clock.Clock
	store          *storage.Store
	fixturesDir    string
	defaultFixture string
}

func (h *resetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.clock != nil {
		h.clock.Reset()
	}

	accs, _ := h.store.ListAccounts(ctx)
	for _, a := range accs {
		_ = h.api.RemoveAccount(ctx, a.ID)
	}
	profs, _ := h.store.ListProfiles(ctx)
	for _, p := range profs {
		_ = h.store.DeleteProfile(ctx, p.ID)
	}
	_ = h.store.EnsureDefaultProfile(ctx)

	if h.mock != nil {
		h.mock.Reset()
	}

	var f mockimap.Fixture
	r.Body = http.MaxBytesReader(w, r.Body, maxTestAPIBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var decode func(any) error
	if len(body) > 0 {
		raw := body
		decode = func(v any) error { return json.Unmarshal(raw, v) }
	}
	if err := loadFixtureFromRequest(h.fixturesDir, h.defaultFixture, r.URL.Query().Get("fixture"), decode, &f); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	applier := &fixtureApplier{api: h.api, mock: h.mock, clock: h.clock, store: h.store}
	if err := applier.apply(ctx, &f); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
