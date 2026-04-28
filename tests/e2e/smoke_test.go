package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	port, _ := freePort(t)
	dir := t.TempDir()
	cmd := exec.Command(bin, "--browser", "--port="+strconv.Itoa(port), "--imap-mock", "--test-api")
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"),
		"XDG_DATA_HOME="+filepath.Join(dir, "data"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)

	// Wait for listener
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := http.Get(base + "/"); err == nil {
			_ = r.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	fixture := []byte(`{"accounts":[{"name":"X","email":"alice@example.com","color":"#fff","use_mock":true,"folders":[{"name":"INBOX"}]}]}`)
	r := postJSON(t, base, "/api/_test/seed", fixture)
	require.Equal(t, http.StatusNoContent, r.StatusCode)
	readBody(r)

	r = postJSON(t, base, "/api/ListAccounts", []byte("{}"))
	var list []map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Equal(t, "X", list[0]["name"])

	// db-dump round-trip (GET — no Origin needed)
	r, _ = http.Get(base + "/api/_test/db-dump")
	var dump map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&dump))
	require.NotEmpty(t, dump["accounts"])
}
