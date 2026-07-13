package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// frontendHandler serves the embedded SPA and injects the per-run API bearer
// token into index.html so the browser bundle can authenticate POST /api/* calls.
func frontendHandler(token string, dist fs.FS) http.Handler {
	inner := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" || p == "/index.html" {
			serveIndexWithToken(w, dist, token)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

func serveIndexWithToken(w http.ResponseWriter, dist fs.FS, token string) {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "index missing", http.StatusInternalServerError)
		return
	}
	tag := fmt.Sprintf(`<meta name="spk-mail-api-token" content="%s">`, token)
	html := strings.Replace(string(data), "</head>", tag+"</head>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The bearer token is per-process-run; a cached index.html would hold a
	// stale token after restart and 401 every /api/* call until the user
	// hard-refreshes. Never let it be cached.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	_, _ = w.Write([]byte(html))
}
