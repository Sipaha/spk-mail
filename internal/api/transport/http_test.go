package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/testapi"
	"github.com/stretchr/testify/require"
)

// failingAPI wraps a real api.API but overrides GetThread to return a leaky
// internal error string the way a real go-imap / sql / fs failure would.
// The httpHandle wrapper must NOT echo this verbatim into the response body.
type failingAPI struct{ api.API }

const leakyError = "imap dial: connect to mail.internal.example.com:993: connection refused"

func (f *failingAPI) GetThread(_ context.Context, _ int64) ([]api.MessageDTO, error) {
	return nil, errors.New(leakyError)
}

// TestHTTP_SanitizesInternalErrors locks in that 500 responses do not echo
// the raw Go error string. A regression that re-introduces err.Error() into
// the body would leak IMAP hostnames, sql driver text, and filesystem paths
// to anyone who pastes a screenshot into a public bug report.
func TestHTTP_SanitizesInternalErrors(t *testing.T) {
	stub := testapi.NewStub(t)
	srv := httptest.NewServer(NewHTTP(&failingAPI{API: stub}, nil))
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/GetThread", bytes.NewReader([]byte(`{"id":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", srv.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.NotContainsf(t, string(body), "mail.internal.example.com",
		"response leaked internal hostname: %s", string(body))
	require.NotContainsf(t, string(body), "imap dial",
		"response leaked Go error string: %s", string(body))
	require.Regexpf(t, regexp.MustCompile(`internal error \([0-9a-f]{8}\)`), string(body),
		"response should carry an opaque correlation id: %s", string(body))
}

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
