package e2e

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
)

// freePort returns a local TCP port the OS just confirmed was free, by
// briefly binding to :0 and closing the listener. There is a small race
// window between the close and the binary picking the port up, but that is
// acceptable for tests — far better than hard-coded ports that race when
// `go test ./...` runs e2e specs concurrently.
func freePort(t *testing.T) (port int, addr string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen :0: %v", err)
	}
	port = l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, "127.0.0.1:" + strconv.Itoa(port)
}

// postJSON POSTs JSON to the binary's API with the same-origin Origin header
// expected by transport.OriginGuard. The guard requires every state-changing
// request to carry Origin (or Referer) matching the server's Host — without
// this helper, raw http.Post would 403.
func postJSON(t *testing.T, base, path string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	return resp
}

// readBody is a tiny helper to drain a response body for tests that don't
// care about the content but want the connection released.
func readBody(r *http.Response) {
	_, _ = io.Copy(io.Discard, r.Body)
	_ = r.Body.Close()
}
