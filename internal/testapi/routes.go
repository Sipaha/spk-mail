package testapi

import (
	"net/http"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

// maxTestAPIBodyBytes caps the request body read by every /api/_test/*
// handler that decodes JSON (or, for inject-message, RFC822 field text).
// Matches the main API's JSON body cap; shared here so the limit can only
// drift in one place.
const maxTestAPIBodyBytes = 1 << 20

type Mount struct {
	API   api.API
	Store *storage.Store
	Mock  *mockimap.Server
	Logs  *RingBuffer
	Clock *clock.Clock
	// FixturesDir + DefaultFixture power POST /api/_test/reset when the
	// caller sends an empty body (Playwright beforeEach). DefaultFixture is
	// the basename under FixturesDir, typically "basic.yaml".
	FixturesDir    string
	DefaultFixture string
}

// Register adds /api/_test/* routes to mux.
func (m *Mount) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/_test/seed", &seedHandler{api: m.API, mock: m.Mock, clock: m.Clock, store: m.Store})
	mux.Handle("POST /api/_test/reset", &resetHandler{
		api: m.API, mock: m.Mock, clock: m.Clock, store: m.Store,
		fixturesDir: m.FixturesDir, defaultFixture: m.DefaultFixture,
	})
	mux.HandleFunc("GET /api/_test/db-dump", dbDumpHandler(m.Store))
	mux.HandleFunc("GET /api/_test/logs", logsHandler(m.Logs))
	mux.Handle("POST /api/_test/inject-message", &injectHandler{mock: m.Mock})
	mux.HandleFunc("POST /api/_test/clock", clockHandler(m.Clock))
}
