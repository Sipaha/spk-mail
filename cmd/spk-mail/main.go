package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/api/transport"
	"github.com/spk/spk-mail/internal/config"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	mailsync "github.com/spk/spk-mail/internal/sync"
	"github.com/spk/spk-mail/internal/testapi"
)

func main() {
	root := &cobra.Command{
		Use:   "spk-mail",
		Short: "Linux desktop email client (IMAP only)",
	}
	var (
		browser  bool
		port     int
		seedPath string
		mockIMAP bool
	)
	root.Flags().BoolVar(&browser, "browser", false, "Run as a localhost HTTP server (no Wails window or tray)")
	root.Flags().IntVar(&port, "port", 5174, "HTTP port for --browser mode")
	root.Flags().StringVar(&seedPath, "seed", "", "Seed YAML fixture at startup (browser mode only)")
	root.Flags().BoolVar(&mockIMAP, "imap-mock", false, "Start an in-process IMAP server (browser mode only)")
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if browser {
			return runBrowser(cmd.Context(), port, mockIMAP, seedPath)
		}
		return runDesktop(cmd.Context())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runBrowser(ctx context.Context, port int, mockIMAP bool, seedPath string) error {
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

	mount := &testapi.Mount{API: stub, Store: st, Mock: mock, Logs: logBuf}
	mount.Register(mux)

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
			})
			if err != nil {
				slog.Warn("seed AddAccount failed", "email", acc.Email, "err", err)
			}
		}
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("spk-mail browser mode listening", "url", "http://"+addr, "data", filepath.Dir(paths.DBFile))

	go func() { <-ctx.Done(); _ = srv.Shutdown(context.Background()) }()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// engineAdapter bridges *mailsync.Engine to the api.Engine interface, avoiding
// an import cycle (internal/sync already depends on internal/api for events).
type engineAdapter struct{ eng *mailsync.Engine }

func (a engineAdapter) StartAccount(ctx context.Context, id int64) { a.eng.StartAccount(ctx, id) }
func (a engineAdapter) StopAccount(id int64)                       { a.eng.StopAccount(id) }
func (a engineAdapter) WorkerFor(id int64) api.FlagOpSubmitter {
	w := a.eng.WorkerFor(id)
	if w == nil {
		return nil
	}
	return workerAdapter{w: w}
}

type workerAdapter struct{ w *mailsync.AccountWorker }

func (a workerAdapter) SubmitFlagOp(op api.FlagOp) {
	a.w.SubmitFlagOp(mailsync.FlagOp{
		AccountID: op.AccountID,
		FolderUID: mailsync.FolderUID{FolderID: op.FolderID, UID: op.UID},
		Add:       op.Add,
		Flags:     op.Flags,
	})
}

// splitHostPort splits "host:port" into host string and port int.
func splitHostPort(addr string) (string, int) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host := addr[:i]
			portStr := addr[i+1:]
			p := 0
			for _, c := range portStr {
				if c >= '0' && c <= '9' {
					p = p*10 + int(c-'0')
				}
			}
			return host, p
		}
	}
	return addr, 0
}
