// Package testapi mounts the /api/_test/* routes used by Playwright + Claude
// Code for UI automation. These routes exist ONLY when the binary is invoked
// with --browser; the desktop build does not register them.
package testapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

type LogEntry struct {
	Time    int64  `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

// RingBuffer is a fixed-size ring of LogEntry rows. The underlying array is
// allocated once at NewRingBuffer time; Append never reslices, so the
// original backing memory cannot leak past `size` entries.
type RingBuffer struct {
	mu    sync.Mutex
	buf   []LogEntry
	size  int
	next  int  // index of the next write slot
	full  bool // true once we have wrapped past `size` writes
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{buf: make([]LogEntry, size), size: size}
}

func (r *RingBuffer) Append(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = e
	r.next++
	if r.next == r.size {
		r.next = 0
		r.full = true
	}
}

func (r *RingBuffer) Snapshot() []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]LogEntry, r.next)
		copy(out, r.buf[:r.next])
		return out
	}
	out := make([]LogEntry, r.size)
	copy(out, r.buf[r.next:])
	copy(out[r.size-r.next:], r.buf[:r.next])
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
	h.rb.Append(LogEntry{Time: time.Now().Unix(), Level: r.Level.String(), Message: formatRecord(r)})
	return h.inner.Handle(ctx, r)
}

// formatRecord renders the slog.Record's message with all attached attrs
// appended as `key=value` pairs. Without this the testapi log buffer drops
// every structured field, so Playwright assertions can match only on the
// hand-written part of slog.Warn(...) calls.
func formatRecord(r slog.Record) string {
	if r.NumAttrs() == 0 {
		return r.Message
	}
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteByte(' ')
		b.WriteString(a.Key)
		b.WriteByte('=')
		fmt.Fprint(&b, a.Value.Any())
		return true
	})
	return b.String()
}
func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlogHandler{inner: h.inner.WithAttrs(attrs), rb: h.rb}
}
func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{inner: h.inner.WithGroup(name), rb: h.rb}
}
