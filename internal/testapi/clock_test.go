package testapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spk/spk-mail/internal/clock"
	"github.com/stretchr/testify/require"
)

func TestClockHandler_FreezeAndReset(t *testing.T) {
	c := clock.New()
	h := clockHandler(c)

	// Freeze
	body, _ := json.Marshal(map[string]any{"now": "2026-04-27T12:00:00Z"})
	req := httptest.NewRequest("POST", "/api/_test/clock", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, c.Now().Equal(time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)))

	// Reset
	body, _ = json.Marshal(map[string]any{"reset": true})
	req = httptest.NewRequest("POST", "/api/_test/clock", bytes.NewReader(body))
	rec = httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.WithinDuration(t, time.Now(), c.Now(), time.Second)
}

func TestClockHandler_BadRequest(t *testing.T) {
	c := clock.New()
	h := clockHandler(c)
	body, _ := json.Marshal(map[string]any{"now": "not-a-date"})
	req := httptest.NewRequest("POST", "/api/_test/clock", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
