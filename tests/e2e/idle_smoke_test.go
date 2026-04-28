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


func TestSmoke_InjectTriggersThread(t *testing.T) {
	bin := os.Getenv("SPKMAIL_BIN")
	if bin == "" {
		bin = "../../build/bin/spk-mail"
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("binary missing (%v); run `make build` first", err)
	}

	port, _ := freePort(t)
	dir := t.TempDir()
	cmd := exec.Command(bin, "--browser", "--port="+strconv.Itoa(port), "--imap-mock", "--test-api", "--seed=../fixtures/basic.yaml")
	cmd.Env = append(os.Environ(),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"),
		"XDG_DATA_HOME="+filepath.Join(dir, "data"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	waitURL(t, base+"/")

	body, _ := json.Marshal(map[string]any{
		"email": "alice@example.com", "from": "Bob <b@x>", "subject": "injected", "body_text": "hello",
	})
	resp := postJSON(t, base, "/api/_test/inject-message", body)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	readBody(resp)

	require.Eventually(t, func() bool {
		r := postJSON(t, base, "/api/ListThreads", []byte("{}"))
		defer r.Body.Close()
		var threads []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&threads); err != nil {
			return false
		}
		for _, th := range threads {
			if th["subject"] == "injected" {
				return true
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "injected thread did not appear")
}
