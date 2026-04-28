//go:build !desktop_only

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/transport"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/config"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	mailsync "github.com/spk/spk-mail/internal/sync"
	"github.com/spk/spk-mail/internal/testapi"
)

func runBrowser(ctx context.Context, port int, mockIMAP bool, seedPath string, testAPI bool) error {
	paths, err := config.Paths()
	if err != nil {
		return err
	}

	logBuf := testapi.NewRingBuffer(500)
	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(testapi.NewSlogHandler(inner, logBuf)))

	st, err := storage.Open(ctx, paths.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	if err := st.EnsureDefaultProfile(ctx); err != nil {
		return fmt.Errorf("ensure default profile: %w", err)
	}

	masterKey, err := secrets.LoadOrCreateMasterKey()
	if err != nil {
		return fmt.Errorf("master key: %w (run desktop mode for password prompt)", err)
	}
	sec, err := secrets.Open(paths.SecretsFile, masterKey)
	if err != nil {
		return err
	}

	em := api.NewEmitter()
	eng := mailsync.NewEngineWithDir(st, sec, em, paths.AttachmentsDir)
	go eng.Run(ctx)
	stub := api.NewStub(st, sec, em, engineAdapter{eng: eng})

	mux := http.NewServeMux()
	httpAPI := transport.NewHTTP(stub, em)
	mux.Handle("/api/", httpAPI)

	var mock *mockimap.Server
	if mockIMAP {
		mock, err = mockimap.Start(ctx, "alice@example.com", "secret")
		if err != nil {
			return fmt.Errorf("mock imap: %w", err)
		}
		defer mock.Close()
		slog.Info("mock IMAP started", "addr", mock.Addr())
	}

	testClock := clock.New()
	// `/api/_test/*` automation routes (db-dump, inject-message, clock
	// override, log buffer) are NEVER mounted by default — they expose the
	// raw DB and let the caller forge inbound mail. They mount only when
	// the explicit --test-api flag is set, which is intended for Playwright
	// runs and ad-hoc development; never enable in production deployments.
	//
	// We wrap the testapi handlers with the same OriginGuard the API mux
	// uses, so a malicious page in the user's browser can't POST to
	// /api/_test/seed or inject-message cross-origin while --test-api is on.
	if testAPI {
		testMux := http.NewServeMux()
		mount := &testapi.Mount{API: stub, Store: st, Mock: mock, Logs: logBuf, Clock: testClock}
		mount.Register(testMux)
		mux.Handle("/api/_test/", transport.OriginGuard(testMux))
		slog.Warn("test-api routes enabled at /api/_test/* — do not expose this server publicly")
	}

	// Serve embedded frontend at /
	mux.Handle("/", http.FileServerFS(frontendFS()))

	if seedPath != "" {
		fixture, err := mockimap.LoadFixture(seedPath)
		if err != nil {
			return err
		}
		if mock != nil {
			_ = mock.Apply(fixture)
		}
		// Look up the bootstrap Default profile so seeded accounts (which
		// have no profile field in the YAML) attach to it. This mirrors
		// migration v2's backfill but applies at runtime-insert time.
		var defaultProfileID *int64
		profs, err := st.ListProfiles(ctx)
		if err == nil {
			for _, p := range profs {
				if p.Name == "Default" {
					id := p.ID
					defaultProfileID = &id
					break
				}
			}
		}
		// also write accounts to DB so they appear in the UI immediately
		for _, acc := range fixture.Accounts {
			pw := acc.Password
			if pw == "" {
				pw = "secret"
			}
			mockHost := ""
			mockPort := 0
			if mock != nil {
				mockHost, mockPort = splitHostPort(mock.Addr())
			}
			_, err := stub.AddAccount(ctx, api.AddAccountRequest{
				Name: acc.Name, Email: acc.Email,
				IMAPHost: mockHost, IMAPPort: mockPort,
				IMAPUsername: acc.Email, IMAPPassword: pw,
				UseTLS: false, Color: acc.Color, UseMock: true,
				ProfileID: defaultProfileID,
			})
			if err != nil {
				slog.Warn("seed AddAccount failed", "email", acc.Email, "err", err)
			}
		}
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	// IdleTimeout caps the keep-alive window between requests so a misbehaving
	// client that opened a connection but never closed it cannot pin a server
	// goroutine forever. 120s is well above any normal browser keep-alive
	// (Chrome reuses for ~5s, Firefox for ~115s) so it doesn't churn live
	// connections. The /api/events SSE handler has its own per-write deadline
	// (sseWriteTimeout) and ignores IdleTimeout because it streams continuously.
	srv := &http.Server{
		Addr: addr, Handler: mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	slog.Info("spk-mail browser mode listening", "url", "http://"+addr, "data", filepath.Dir(paths.DBFile))

	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// splitHostPort splits "host:port" into host string and port int. Supports
// IPv6 literals via standard library bracket parsing, e.g. "[::1]:5432".
func splitHostPort(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	p, _ := strconv.Atoi(portStr)
	return host, p
}
