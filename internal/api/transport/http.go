package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spk/spk-mail/internal/api"
)

type HTTP struct {
	api    api.API
	events *api.Emitter
	mux    *http.ServeMux
}

func NewHTTP(a api.API, em *api.Emitter) *HTTP {
	h := &HTTP{api: a, events: em, mux: http.NewServeMux()}
	h.routes()
	return h
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

func (h *HTTP) routes() {
	h.mux.HandleFunc("POST /api/ListAccounts", httpHandle(func(ctx context.Context, _ *struct{}) (any, error) { return h.api.ListAccounts(ctx) }))
	h.mux.HandleFunc("POST /api/AddAccount", httpHandle(func(ctx context.Context, req *api.AddAccountRequest) (any, error) { return h.api.AddAccount(ctx, *req) }))
	h.mux.HandleFunc("POST /api/RemoveAccount", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return nil, h.api.RemoveAccount(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/ListThreads", httpHandle(func(ctx context.Context, req *api.ThreadFilter) (any, error) { return h.api.ListThreads(ctx, *req) }))
	h.mux.HandleFunc("POST /api/GetThread", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return h.api.GetThread(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/MarkRead", httpHandle(func(ctx context.Context, req *struct {
		IDs []int64 `json:"ids"`
	}) (any, error) {
		return nil, h.api.MarkRead(ctx, req.IDs)
	}))
	h.mux.HandleFunc("POST /api/AllowRemoteForMessage", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return h.api.AllowRemoteForMessage(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/Search", httpHandle(func(ctx context.Context, req *struct {
		Query  string `json:"query"`
		Limit  int
		Offset int
	}) (any, error) {
		return h.api.Search(ctx, req.Query, req.Limit, req.Offset)
	}))
	h.mux.HandleFunc("POST /api/UnreadCounts", httpHandle(func(ctx context.Context, _ *struct{}) (any, error) { return h.api.UnreadCounts(ctx) }))

	h.mux.HandleFunc("GET /api/events", h.sse)
}

func httpHandle[Req any](fn func(context.Context, *Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		out, err := fn(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (h *HTTP) sse(w http.ResponseWriter, r *http.Request) {
	if h.events == nil {
		http.Error(w, "events disabled", http.StatusNotImplemented)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ch, unsub := h.events.Subscribe()
	defer unsub()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
