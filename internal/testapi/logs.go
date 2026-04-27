// Package testapi mounts the /api/_test/* routes used by Playwright + Claude
// Code for UI automation. These routes exist ONLY when the binary is invoked
// with --browser; the desktop build does not register them.
package testapi

import (
	"encoding/json"
	"net/http"
	"sync"
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
