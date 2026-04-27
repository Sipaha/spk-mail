package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSmoke_BrowserMode launches the built binary in --browser --imap-mock mode,
// posts a fixture, and verifies it lands in the API.
func TestSmoke_BrowserMode(t *testing.T) {
	bin := os.Getenv("SPKMAIL_BIN")
	if bin == "" {
		bin = "../../build/bin/spk-mail"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binary missing (%v); run `make build` first", err)
	}

	dir := t.TempDir()
	cmd := exec.Command(bin, "--browser", "--port=5188", "--imap-mock")
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"),
		"XDG_DATA_HOME="+filepath.Join(dir, "data"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	// Wait for listener
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := http.Get("http://127.0.0.1:5188/"); err == nil {
			_ = r.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	fixture := []byte(`{"accounts":[{"name":"X","email":"alice@example.com","color":"#fff","use_mock":true,"folders":[{"name":"INBOX"}]}]}`)
	r, err := http.Post("http://127.0.0.1:5188/api/_test/seed", "application/json", bytes.NewReader(fixture))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, r.StatusCode)

	r, err = http.Post("http://127.0.0.1:5188/api/ListAccounts", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	var list []map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Equal(t, "X", list[0]["name"])

	// db-dump round-trip
	r, _ = http.Get("http://127.0.0.1:5188/api/_test/db-dump")
	var dump map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&dump))
	require.NotEmpty(t, dump["accounts"])
}
