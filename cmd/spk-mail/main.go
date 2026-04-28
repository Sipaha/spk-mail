package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/spk/spk-mail/internal/api"
	mailsync "github.com/spk/spk-mail/internal/sync"
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
		testAPI  bool
	)
	root.Flags().BoolVar(&browser, "browser", false, "Run as a localhost HTTP server (no Wails window or tray)")
	root.Flags().IntVar(&port, "port", 5174, "HTTP port for --browser mode")
	root.Flags().StringVar(&seedPath, "seed", "", "Seed YAML fixture at startup (browser mode only)")
	root.Flags().BoolVar(&mockIMAP, "imap-mock", false, "Start an in-process IMAP server (browser mode only)")
	root.Flags().BoolVar(&testAPI, "test-api", false, "Expose /api/_test/* automation routes (Playwright/development only — never use in production)")
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if browser {
			return runBrowser(cmd.Context(), port, mockIMAP, seedPath, testAPI)
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
