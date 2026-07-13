package transport

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// AuthToken returns the per-process bearer token required on browser-mode API
// routes. The UI reads it from an injected <meta> tag in index.html.
func (h *HTTP) AuthToken() string { return h.authToken }

func newAuthToken() string { return randomHex(32) }

// tokenEq compares in constant time so a wrong token cannot be recovered
// byte-by-byte from response timing.
func tokenEq(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func bearerOK(r *http.Request, token string) bool {
	return tokenEq(r.Header.Get("Authorization"), "Bearer "+token)
}

func (h *HTTP) authorized(r *http.Request, path string) bool {
	if h.authToken == "" {
		return true
	}
	if bearerOK(r, h.authToken) {
		return true
	}
	// EventSource cannot set Authorization; allow query token only on SSE.
	if path == "/api/events" && tokenEq(r.URL.Query().Get("token"), h.authToken) {
		return true
	}
	return false
}

// AuthGuard wraps next with bearer-token auth (used for /api/_test/* mux).
//
// The query-token fallback is limited to GET/HEAD (the read-only db-dump / logs
// routes, which are handy to open straight in a browser). State-mutating routes
// — seed, reset, inject-message, clock — require the Authorization header, so a
// token cannot leak into access logs or browser history from a request that
// changes state.
func AuthGuard(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := bearerOK(r, token)
		if !ok && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			ok = tokenEq(r.URL.Query().Get("token"), token)
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func apiPath(path string) bool {
	return strings.HasPrefix(path, "/api/")
}
