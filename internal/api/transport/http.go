package transport

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/api"
)

type HTTP struct {
	api    api.API
	events *api.Emitter
	mux    *http.ServeMux
}

func NewHTTP(a api.API, em *api.Emitter) *HTTP {
	h := &HTTP{api: a, events: em, mux: http.NewServeMux()}
	h.routes()
	return h
}

func (h *HTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Delegate the CSRF check to OriginGuard so the API mux and the
	// `/api/_test/*` testapi mux apply identical rules.
	OriginGuard(h.mux).ServeHTTP(w, r)
}

// OriginGuard wraps h with a CSRF check: state-changing methods (POST, PUT,
// PATCH, DELETE) must carry an Origin OR Referer header that matches the
// server's Host. State-reading methods (GET, HEAD) are permitted without
// either header so curl-style probing of `/api/events` and friends keeps
// working. The pair-of-headers rule mirrors OWASP's "verify same-origin
// with standard headers" guidance — modern browsers reliably attach Origin
// (or at least Referer) to cross-site state-changing requests.
//
// Loopback aliases (localhost ↔ 127.0.0.1 ↔ ::1) are honored exactly via
// originMatchesHost; prefix-matching is intentionally not used (it was
// previously bypassed by `Origin: http://localhostevil:PORT` and re-added
// here would re-introduce that bypass).
func OriginGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		mutating := r.Method == http.MethodPost || r.Method == http.MethodPut ||
			r.Method == http.MethodPatch || r.Method == http.MethodDelete
		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		if mutating && origin == "" && referer == "" {
			http.Error(w, "missing Origin/Referer on state-changing request", http.StatusForbidden)
			return
		}
		if origin != "" && !originMatchesHost(origin, r.Host) {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		// Origin absent but Referer present (legacy / WebView edge cases) —
		// validate the Referer's origin instead. Same exact-host rule.
		if origin == "" && referer != "" && mutating {
			if !originMatchesHost(referer, r.Host) {
				http.Error(w, "cross-origin Referer blocked", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

// originMatchesHost is true when the URL in the Origin header has the same
// host:port as r.Host. Comparison is on the parsed Hostname() to prevent
// prefix-match bypasses like `Origin: http://localhostevil:5174` matching a
// `Host: 127.0.0.1:5174` server.
func originMatchesHost(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, host) {
		return true
	}
	originHost := strings.ToLower(u.Hostname())
	originPort := u.Port()
	hostHost, hostPort, err := splitHostPortLoose(host)
	if err != nil {
		return false
	}
	hostHost = strings.ToLower(hostHost)
	if originPort != hostPort {
		return false
	}
	// Loopback aliases — exact match only, no prefix.
	loopbackAliases := map[string]map[string]bool{
		"localhost": {"127.0.0.1": true, "::1": true},
		"127.0.0.1": {"localhost": true},
		"::1":       {"localhost": true},
	}
	if aliases, ok := loopbackAliases[originHost]; ok && aliases[hostHost] {
		return true
	}
	return false
}

// splitHostPortLoose accepts both "host:port" and a bare "host" (no port).
// Returns the parsed hostname (without IPv6 brackets) and port string.
func splitHostPortLoose(s string) (string, string, error) {
	if h, p, err := splitHostPort(s); err == nil {
		return h, p, nil
	}
	// No port present — return host as-is.
	return strings.Trim(s, "[]"), "", nil
}

func splitHostPort(s string) (string, string, error) {
	// net.SplitHostPort would be ideal but it errors on bare host; we want
	// loose parsing. Find the LAST colon that isn't inside brackets.
	if strings.HasPrefix(s, "[") {
		if end := strings.Index(s, "]"); end > 0 {
			port := ""
			if len(s) > end+1 && s[end+1] == ':' {
				port = s[end+2:]
			}
			return s[1:end], port, nil
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i], s[i+1:], nil
	}
	return "", "", fmt.Errorf("no port")
}

func (h *HTTP) routes() {
	h.mux.HandleFunc("POST /api/ListAccounts", httpHandle(func(ctx context.Context, _ *struct{}) (any, error) { return h.api.ListAccounts(ctx) }))
	h.mux.HandleFunc("POST /api/AddAccount", httpHandle(func(ctx context.Context, req *api.AddAccountRequest) (any, error) { return h.api.AddAccount(ctx, *req) }))
	h.mux.HandleFunc("POST /api/RemoveAccount", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return nil, h.api.RemoveAccount(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/ListFolders", httpHandle(func(ctx context.Context, req *struct {
		AccountID int64 `json:"account_id"`
	}) (any, error) {
		return h.api.ListFolders(ctx, req.AccountID)
	}))
	h.mux.HandleFunc("POST /api/ListThreads", httpHandle(func(ctx context.Context, req *api.ThreadFilter) (any, error) { return h.api.ListThreads(ctx, *req) }))
	h.mux.HandleFunc("POST /api/GetThread", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return h.api.GetThread(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/MarkRead", httpHandle(func(ctx context.Context, req *struct {
		IDs []int64 `json:"ids"`
	}) (any, error) {
		return nil, h.api.MarkRead(ctx, req.IDs)
	}))
	h.mux.HandleFunc("POST /api/MarkFolderRead", httpHandle(func(ctx context.Context, req *struct {
		FolderID int64 `json:"folder_id"`
	}) (any, error) {
		n, err := h.api.MarkFolderRead(ctx, req.FolderID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"count": n}, nil
	}))
	h.mux.HandleFunc("POST /api/ToggleThreadFlagged", httpHandle(func(ctx context.Context, req *struct {
		ThreadID int64 `json:"thread_id"`
	}) (any, error) {
		return h.api.ToggleThreadFlagged(ctx, req.ThreadID)
	}))
	h.mux.HandleFunc("POST /api/AllowRemoteForMessage", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return h.api.AllowRemoteForMessage(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/Search", httpHandle(func(ctx context.Context, req *struct {
		Query  string `json:"query"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}) (any, error) {
		return h.api.Search(ctx, req.Query, req.Limit, req.Offset)
	}))
	h.mux.HandleFunc("POST /api/OpenAttachment", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return nil, h.api.OpenAttachment(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/ListProfiles", httpHandle(func(ctx context.Context, _ *struct{}) (any, error) { return h.api.ListProfiles(ctx) }))
	h.mux.HandleFunc("POST /api/AddProfile", httpHandle(func(ctx context.Context, req *api.AddProfileRequest) (any, error) { return h.api.AddProfile(ctx, *req) }))
	h.mux.HandleFunc("POST /api/UpdateProfile", httpHandle(func(ctx context.Context, req *api.UpdateProfileRequest) (any, error) {
		return h.api.UpdateProfile(ctx, *req)
	}))
	h.mux.HandleFunc("POST /api/DeleteProfile", httpHandle(func(ctx context.Context, req *struct {
		ID int64 `json:"id"`
	}) (any, error) {
		return nil, h.api.DeleteProfile(ctx, req.ID)
	}))
	h.mux.HandleFunc("POST /api/SetProfileMuted", httpHandle(func(ctx context.Context, req *struct {
		ID    int64 `json:"id"`
		Muted bool  `json:"muted"`
	}) (any, error) {
		return nil, h.api.SetProfileMuted(ctx, req.ID, req.Muted)
	}))

	h.mux.HandleFunc("GET /api/events", h.sse)
}

// maxAPIRequestBytes bounds JSON request bodies. The largest current API
// request is `seed` with a fixture payload; 1 MiB is generously above any
// realistic body and prevents a same-origin caller from holding the request
// goroutine for minutes while the SQLite write lock contends.
const maxAPIRequestBytes = 1 << 20

func httpHandle[Req any](fn func(context.Context, *Req) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxAPIRequestBytes)
			if r.ContentLength != 0 {
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		out, err := fn(r.Context(), &req)
		if err != nil {
			// Don't echo err.Error() into the response body. Go errors here
			// often carry IMAP hostnames, filesystem paths, sql driver text,
			// and other internals that aren't useful to the user but ARE
			// useful to anyone who pastes a screenshot into a public bug
			// report. Log the full error server-side, return a short sentinel
			// plus a correlation id the user can quote when reporting.
			id := newErrorID()
			slog.Error("api handler failed",
				"path", r.URL.Path, "method", r.Method,
				"err_id", id, "err", err.Error())
			http.Error(w, "internal error ("+id+")", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// newErrorID returns a short opaque identifier suitable for printing in an
// HTTP error body so a user-reported "internal error (deadbeef)" can be
// grepped against the logs. 4 random bytes (8 hex chars) is wide enough to
// disambiguate within a single log buffer; collisions across runs do not
// matter because logs are rotated.
func newErrorID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure on Linux is a kernel-level emergency; fall
		// back to a fixed id so we don't return an empty string and lose
		// the visible signal in the response.
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// ssePingInterval bounds how long the connection can stay idle before we
// poke it with a comment frame. 25s is the conventional default — short
// enough to detect a silently-dead TCP socket inside a minute, long enough
// not to wake mobile radios needlessly. EventSource clients ignore lines
// that begin with ":" (SSE comment frames) per the WHATWG spec, so the
// keepalive is invisible at the application layer.
const ssePingInterval = 25 * time.Second

// sseWriteTimeout caps each individual write so a half-broken peer (e.g.
// firewall ate the FIN, OS kernel hasn't decided the socket is dead yet)
// can't pin the goroutine + the 64-cap subscriber channel forever. On
// timeout the write returns an error and we exit the loop cleanly,
// freeing the subscription via the deferred unsub.
const sseWriteTimeout = 10 * time.Second

func (h *HTTP) sse(w http.ResponseWriter, r *http.Request) {
	if h.events == nil {
		http.Error(w, "events disabled", http.StatusNotImplemented)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	rc := http.NewResponseController(w)

	ch, unsub := h.events.Subscribe()
	defer unsub()

	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
				return
			}
			flusher.Flush()
		case <-ping.C:
			// SSE comment frame — invisible to the application but forces a
			// real TCP write, which surfaces a dead peer as a Write error.
			_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
