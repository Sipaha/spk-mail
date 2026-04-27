//go:build wails

package main

import (
	"context"
	"fmt"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/appfiles"
	"github.com/spk/spk-mail/internal/config"
	"github.com/spk/spk-mail/internal/desktop"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	mailsync "github.com/spk/spk-mail/internal/sync"
)

func runDesktop(ctx context.Context) error {
	paths, err := config.Paths()
	if err != nil {
		return err
	}

	st, err := storage.Open(ctx, paths.DBFile)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	masterKey, err := secrets.LoadOrCreateMasterKey()
	if err != nil {
		return fmt.Errorf("master key: %w", err)
	}
	sec, err := secrets.Open(paths.SecretsFile, masterKey)
	if err != nil {
		return err
	}

	em := api.NewEmitter()
	eng := mailsync.NewEngine(st, sec, em)
	go eng.Run(ctx)

	stub := api.NewStub(st, sec, em, engineAdapter{eng: eng})

	icon, _ := appfiles.FS.ReadFile("icons/spk-mail.png")
	return desktop.Run(ctx, desktop.Options{
		FrontendFS: frontendFS(),
		API:        stub,
		Emitter:    em,
		IconPNG:    icon,
	})
}
