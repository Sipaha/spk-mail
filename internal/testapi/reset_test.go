package testapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/api"
	apitestapi "github.com/spk/spk-mail/internal/api/testapi"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestReset_RestoresDefaultFixture(t *testing.T) {
	stub := apitestapi.NewStub(t)
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	store := stub.(*api.Stub).Store.(*storage.Store)
	wd, err := os.Getwd()
	require.NoError(t, err)
	fixturesDir := filepath.Join(wd, "..", "..", "tests", "fixtures")
	require.DirExists(t, fixturesDir)
	m := &Mount{
		API: stub, Store: store, Mock: mock, Logs: NewRingBuffer(100),
		FixturesDir: fixturesDir, DefaultFixture: "basic.yaml",
	}
	mux := http.NewServeMux()
	m.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Seed an extra account so the DB is dirty.
	extra := mockimap.Fixture{Accounts: []mockimap.FixtureAccount{
		{Name: "Extra", Email: "extra@example.com", Color: "#000", UseMock: true,
			Folders: []mockimap.FixtureFolder{{Name: "INBOX"}}},
	}}
	body, _ := json.Marshal(extra)
	resp, err := http.Post(srv.URL+"/api/_test/seed", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Post(srv.URL+"/api/_test/reset", "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/_test/db-dump")
	require.NoError(t, err)
	var dump map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dump))
	accs, _ := dump["accounts"].([]any)
	require.Len(t, accs, 1)
	first, _ := accs[0].(map[string]any)
	require.Equal(t, "Test Personal", first["name"])
}

func TestReset_QueryFixture(t *testing.T) {
	stub := apitestapi.NewStub(t)
	mock, err := mockimap.Start(context.Background(), "alice@example.com", "secret")
	require.NoError(t, err)
	defer mock.Close()

	store := stub.(*api.Stub).Store.(*storage.Store)
	wd, err := os.Getwd()
	require.NoError(t, err)
	fixturesDir := filepath.Join(wd, "..", "..", "tests", "fixtures")
	require.FileExists(t, filepath.Join(fixturesDir, "multi-account.yaml"))

	m := &Mount{
		API: stub, Store: store, Mock: mock, Logs: NewRingBuffer(100),
		FixturesDir: fixturesDir, DefaultFixture: "basic.yaml",
	}
	mux := http.NewServeMux()
	m.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/_test/reset?fixture=multi-account.yaml", "application/json", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/_test/db-dump")
	require.NoError(t, err)
	var dump map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dump))
	accs, _ := dump["accounts"].([]any)
	require.Len(t, accs, 2)
}
