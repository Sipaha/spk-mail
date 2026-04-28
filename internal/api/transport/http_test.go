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
