package testapi

import (
	"net/http"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

type Mount struct {
	API   api.API
	Store *storage.Store
	Mock  *mockimap.Server
	Logs  *RingBuffer
	Clock *clock.Clock
}

// Register adds /api/_test/* routes to mux.
func (m *Mount) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/_test/seed", &seedHandler{api: m.API, mock: m.Mock, clock: m.Clock, store: m.Store})
	mux.HandleFunc("GET /api/_test/db-dump", dbDumpHandler(m.Store))
	mux.HandleFunc("GET /api/_test/logs", logsHandler(m.Logs))
	mux.Handle("POST /api/_test/inject-message", &injectHandler{mock: m.Mock})
	mux.HandleFunc("POST /api/_test/clock", clockHandler(m.Clock))
}
