package testapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	apitestapi "github.com/spk/spk-mail/internal/api/testapi"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestSeed_AccountAppearsInDBDump(t *testing.T) {
	stub := apitestapi.NewStub(t)
	dir := t.TempDir()
	store, _ := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	defer store.Close()
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	m := &Mount{API: stub, Store: store, Mock: mock, Logs: NewRingBuffer(100)}
	mux := http.NewServeMux()
	m.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fixture := mockimap.Fixture{Accounts: []mockimap.FixtureAccount{
		{Name: "Alice", Email: "alice@example.com", Color: "#fff", UseMock: true,
			Folders: []mockimap.FixtureFolder{{Name: "INBOX"}}},
	}}
	body, _ := json.Marshal(fixture)
	resp, err := http.Post(srv.URL+"/api/_test/seed", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, err = http.Get(srv.URL + "/api/_test/db-dump")
	require.NoError(t, err)
	var dump map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dump))
	accs, _ := dump["accounts"].([]any)
	require.Len(t, accs, 1)
}
