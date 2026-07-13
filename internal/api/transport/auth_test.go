package transport

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spk/spk-mail/internal/api/testapi"
	"github.com/spk/spk-mail/internal/events"
	"github.com/stretchr/testify/require"
)

// TestHTTP_ServeHTTP_RequiresBearerToken exercises HTTP.ServeHTTP directly
// (not AuthGuard) on a plain /api/... route: no Authorization header must be
// rejected, a wrong bearer token must be rejected, and the correct bearer
// token must get past the auth check (it may still fail later for unrelated
// reasons, but must not be a 401).
func TestHTTP_ServeHTTP_RequiresBearerToken(t *testing.T) {
	stub := testapi.NewStub(t)
	h := NewHTTP(stub, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	post := func(headers map[string]string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ListAccounts", bytes.NewReader([]byte("{}")))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", srv.URL)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp
	}

	t.Run("no Authorization header", func(t *testing.T) {
		resp := post(nil)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("wrong bearer token", func(t *testing.T) {
		resp := post(map[string]string{"Authorization": "Bearer wrong-token"})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("correct bearer token", func(t *testing.T) {
		resp := post(map[string]string{"Authorization": authHeader(h)})
		require.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
	})
}

// TestHTTP_ServeHTTP_QueryTokenOnlyOnEventsRoute locks in that the ?token=
// query-string fallback (needed because EventSource cannot set a request
// header) is honored ONLY on /api/events; any other /api/* route must reject
// a bare query token and demand the Authorization header instead.
func TestHTTP_ServeHTTP_QueryTokenOnlyOnEventsRoute(t *testing.T) {
	stub := testapi.NewStub(t)
	em := events.NewEmitter()
	h := NewHTTP(stub, em)
	srv := httptest.NewServer(h)
	defer srv.Close()

	// /api/events: ?token= alone, no Authorization header, is accepted.
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/events?token="+h.AuthToken(), nil)
	require.NoError(t, err)
	req.Header.Set("Origin", srv.URL)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Any other /api/* route: ?token= alone is rejected with 401.
	req2, err := http.NewRequest(http.MethodPost, srv.URL+"/api/ListAccounts?token="+h.AuthToken(), bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Origin", srv.URL)
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	_ = resp2.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp2.StatusCode)
}

// TestAuthGuard covers the AuthGuard middleware used for the /api/_test/*
// mux: the Authorization header works for both GET and POST, the ?token=
// query fallback works only for GET/HEAD, mutating (POST) requests must
// carry the header even if a valid query token is present, and an empty
// configured token turns the guard into a pass-through.
func TestAuthGuard(t *testing.T) {
	const token = "secret-test-token"
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	call := func(guard http.Handler, method, target string, headers map[string]string) int {
		req := httptest.NewRequest(method, target, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		guard.ServeHTTP(rec, req)
		return rec.Code
	}

	guard := AuthGuard(token, ok)

	t.Run("bearer header passes GET", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump",
			map[string]string{"Authorization": "Bearer " + token})
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("bearer header passes POST", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed",
			map[string]string{"Authorization": "Bearer " + token})
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token passes GET", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump?token="+token, nil)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token passes HEAD", func(t *testing.T) {
		code := call(guard, http.MethodHead, "/api/_test/db-dump?token="+token, nil)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("query token rejected on POST", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed?token="+token, nil)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("wrong bearer token rejected", func(t *testing.T) {
		code := call(guard, http.MethodGet, "/api/_test/db-dump",
			map[string]string{"Authorization": "Bearer wrong-token"})
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("missing credentials rejected", func(t *testing.T) {
		code := call(guard, http.MethodPost, "/api/_test/seed", nil)
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("empty configured token is pass-through", func(t *testing.T) {
		passThrough := AuthGuard("", ok)
		code := call(passThrough, http.MethodPost, "/api/_test/seed", nil)
		require.Equal(t, http.StatusOK, code)
	})
}
