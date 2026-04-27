package testapi

import (
	"net/http"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

type Mount struct {
	API   api.API
	Store *storage.Store
	Mock  *mockimap.Server
	Logs  *RingBuffer
}

// Register adds /api/_test/* routes to mux.
func (m *Mount) Register(mux *http.ServeMux) {
	mux.Handle("POST /api/_test/seed", &seedHandler{api: m.API, mock: m.Mock})
	mux.HandleFunc("GET /api/_test/db-dump", dbDumpHandler(m.API, m.Store))
	mux.HandleFunc("GET /api/_test/logs", logsHandler(m.Logs))
	mux.Handle("POST /api/_test/inject-message", &injectHandler{mock: m.Mock})
	// clock is filled in plan 7
	mux.HandleFunc("POST /api/_test/clock", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "implemented in plan 7", http.StatusNotImplemented)
	})
}
