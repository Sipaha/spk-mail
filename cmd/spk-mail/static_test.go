package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestServeIndexWithToken_NoStore: the bearer token is minted fresh on
// every process start, so a browser-cached index.html would keep POSTing
// /api/* with a token the current run no longer recognizes (401 loop until
// a hard refresh). The response must be marked non-cacheable.
func TestServeIndexWithToken_NoStore(t *testing.T) {
	dist := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")},
	}

	w := httptest.NewRecorder()
	serveIndexWithToken(w, dist, "tok-123")

	resp := w.Result()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-store")
	}
	if got := resp.Header.Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want %q", got, "no-cache")
	}
	body := w.Body.String()
	if want := `<meta name="spk-mail-api-token" content="tok-123">`; !strings.Contains(body, want) {
		t.Errorf("body missing token meta tag; got %q", body)
	}
}
