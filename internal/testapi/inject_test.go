package testapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/stretchr/testify/require"
)

func TestInjectHandler_RejectsOversizedBody(t *testing.T) {
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	h := &injectHandler{mock: mock}
	body := bytes.NewReader([]byte(`{"email":"alice@example.com","from":"x","subject":"s","body_text":"` + strings.Repeat("x", 1<<20) + `"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/_test/inject-message", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
