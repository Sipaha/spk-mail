package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/testapi"
	"github.com/stretchr/testify/require"
)

// TestHTTP_TestAPIRoutesNotMountedByDefault locks in the rule that the
// /api/_test/* automation surface (db-dump, inject-message, log buffer …)
// is opt-in via the binary's --test-api flag. NewHTTP itself never registers
// those handlers, so a request must 404 rather than reach the testapi.Mount.
// A regression here would silently expose the raw DB on a production deploy.
func TestHTTP_TestAPIRoutesNotMountedByDefault(t *testing.T) {
	stub := testapi.NewStub(t)
	srv := httptest.NewServer(NewHTTP(stub, nil))
	defer srv.Close()

	for _, path := range []string{
		"/api/_test/db-dump",
		"/api/_test/inject-message",
		"/api/_test/logs",
	} {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("Origin", srv.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equalf(t, http.StatusNotFound, resp.StatusCode,
			"path %q must return 404 without --test-api gate", path)
	}
}

func TestHTTP_AddAccountThenList(t *testing.T) {
	stub := testapi.NewStub(t)
	srv := httptest.NewServer(NewHTTP(stub, nil))
	defer srv.Close()

	// Helper: POST with a same-origin Origin header so OriginGuard accepts.
	post := func(path string, body []byte) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", srv.URL)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		return resp
	}

	body, _ := json.Marshal(api.AddAccountRequest{
		Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "p", UseTLS: true, Color: "#fff",
	})
	resp := post("/api/AddAccount", body)
	require.Equal(t, 200, resp.StatusCode)
	var dto api.AccountDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Greater(t, dto.ID, int64(0))

	resp = post("/api/ListAccounts", []byte("{}"))
	var list []api.AccountDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list, 1)
}
