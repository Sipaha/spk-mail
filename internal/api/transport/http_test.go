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

	body, _ := json.Marshal(api.AddAccountRequest{
		Name: "A", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", IMAPPassword: "p", UseTLS: true, Color: "#fff",
	})
	resp, err := http.Post(srv.URL+"/api/AddAccount", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	var dto api.AccountDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dto))
	require.Greater(t, dto.ID, int64(0))

	resp, err = http.Post(srv.URL+"/api/ListAccounts", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	var list []api.AccountDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&list))
	require.Len(t, list, 1)
}
