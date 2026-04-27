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

func TestSmoke_InjectTriggersThread(t *testing.T) {
	bin := os.Getenv("SPKMAIL_BIN")
	if bin == "" {
		bin = "../../build/bin/spk-mail"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binary missing (%v); run `make build` first", err)
	}

	dir := t.TempDir()
	cmd := exec.Command(bin, "--browser", "--port=5189", "--imap-mock", "--seed=../fixtures/basic.yaml")
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"),
		"XDG_DATA_HOME="+filepath.Join(dir, "data"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	waitURL(t, "http://127.0.0.1:5189/")

	body, _ := json.Marshal(map[string]any{
		"email": "alice@example.com", "from": "Bob <b@x>", "subject": "injected", "body_text": "hello",
	})
	resp, err := http.Post("http://127.0.0.1:5189/api/_test/inject-message", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := http.Post("http://127.0.0.1:5189/api/ListThreads", "application/json", bytes.NewReader([]byte("{}")))
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		var threads []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&threads); err == nil {
			for _, th := range threads {
				if th["subject"] == "injected" {
					return
				}
			}
		}
		_ = r.Body.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("injected thread did not appear")
}

func waitURL(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := http.Get(url); err == nil {
			_ = r.Body.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server did not start")
}
