// Package testapi mounts the /api/_test/* routes used by Playwright + Claude
// Code for UI automation. These routes exist ONLY when the binary is invoked
// with --browser; the desktop build does not register them.
package testapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type LogEntry struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type RingBuffer struct {
	mu  sync.Mutex
	buf []LogEntry
	cap int
}

func NewRingBuffer(cap int) *RingBuffer { return &RingBuffer{cap: cap} }

func (r *RingBuffer) Append(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) >= r.cap {
		r.buf = r.buf[1:]
	}
	r.buf = append(r.buf, e)
}

func (r *RingBuffer) Snapshot() []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LogEntry, len(r.buf))
	copy(out, r.buf)
	return out
}

func logsHandler(rb *RingBuffer) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rb.Snapshot())
	}
}

// SlogHandler wraps another slog.Handler and mirrors records into a RingBuffer.
type SlogHandler struct {
	inner slog.Handler
	rb    *RingBuffer
}

func NewSlogHandler(inner slog.Handler, rb *RingBuffer) *SlogHandler {
	return &SlogHandler{inner: inner, rb: rb}
}

func (h *SlogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}
func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	h.rb.Append(LogEntry{Time: time.Now().Unix(), Level: r.Level.String(), Message: r.Message})
	return h.inner.Handle(ctx, r)
}
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlogHandler{inner: h.inner.WithAttrs(attrs), rb: h.rb}
}
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{inner: h.inner.WithGroup(name), rb: h.rb}
}
