package main

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestResolveMasterKey_PasswordDeterministicAndIsolated(t *testing.T) {
	dir := t.TempDir()
	secretsFile := filepath.Join(dir, "secrets.bin")
	// Even if a bus address exists or points nowhere, SPK_MAIL_PASSWORD must
	// derive deterministically from master.salt without using OS keyring.
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:abstract=spk-mail-nonexistent-test-bus")
	t.Setenv("SPK_MAIL_PASSWORD", "test-master-password")

	k1, err := resolveMasterKey(secretsFile)
	if err != nil {
		t.Fatalf("resolveMasterKey #1 failed: %v", err)
	}
	if len(k1) != 32 {
		t.Fatalf("key length = %d, want 32", len(k1))
	}
	if _, err := os.Stat(filepath.Join(dir, "master.salt")); err != nil {
		t.Fatalf("master.salt missing: %v", err)
	}

	k2, err := resolveMasterKey(secretsFile)
	if err != nil {
		t.Fatalf("resolveMasterKey #2 failed: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("expected deterministic key reuse across invocations")
	}

	t.Setenv("SPK_MAIL_PASSWORD", "different-password")
	k3, err := resolveMasterKey(secretsFile)
	if err != nil {
		t.Fatalf("resolveMasterKey #3 failed: %v", err)
	}
	if bytes.Equal(k1, k3) {
		t.Fatalf("expected distinct password to derive a distinct key")
	}
}
