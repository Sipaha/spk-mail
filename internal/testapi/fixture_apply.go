package testapi

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/clock"
	"github.com/spk/spk-mail/internal/mockimap"
	"github.com/spk/spk-mail/internal/storage"
)

type fixtureApplier struct {
	api   api.API
	mock  *mockimap.Server
	clock *clock.Clock
	store *storage.Store
}

func (h *fixtureApplier) apply(ctx context.Context, f *mockimap.Fixture) error {
	if h.mock != nil {
		var ns mockimap.NowSource
		if h.clock != nil {
			ns = h.clock
		}
		if err := h.mock.ApplyWithClock(f, ns); err != nil {
			return fmt.Errorf("mock apply: %w", err)
		}
	}

	var defaultProfileID int64
	if h.store != nil {
		if profiles, err := h.store.ListProfiles(ctx); err == nil && len(profiles) > 0 {
			defaultProfileID = profiles[0].ID
		}
	}

	for _, acc := range f.Accounts {
		host, port := mockHostPort(h.mock)
		pw := acc.Password
		if pw == "" {
			pw = "secret"
		}
		req := api.AddAccountRequest{
			Name:         acc.Name,
			Email:        acc.Email,
			IMAPHost:     host,
			IMAPPort:     port,
			IMAPUsername: acc.Email,
			IMAPPassword: pw,
			UseTLS:       false,
			Color:        acc.Color,
			UseMock:      true,
		}
		if defaultProfileID > 0 {
			pid := defaultProfileID
			req.ProfileID = &pid
		}
		if _, err := h.api.AddAccount(ctx, req); err != nil {
			return fmt.Errorf("add account %s: %w", acc.Email, err)
		}
	}
	return nil
}

func loadFixtureFromRequest(fixturesDir, defaultFixture, queryFixture string, decode func(any) error, target *mockimap.Fixture) error {
	if decode != nil {
		if err := decode(target); err != nil {
			return fmt.Errorf("decode fixture body: %w", err)
		}
		return nil
	}
	name := queryFixture
	if name == "" {
		name = defaultFixture
	}
	if name == "" || fixturesDir == "" {
		return fmt.Errorf("no fixture body and no default fixture configured")
	}
	// filepath.Base strips any directory components from name, so path is
	// always a direct child of fixturesDir: the worst a crafted name (e.g.
	// "..") can do is collapse to fixturesDir's parent *directory* itself,
	// never an arbitrary file elsewhere, and mockimap.LoadFixture below
	// fails on that (it's a directory, not a fixture file). A separate
	// filepath.Rel/HasPrefix containment check here would be unreachable
	// for anything Base doesn't already neutralize, so we don't add one.
	path := filepath.Join(fixturesDir, filepath.Base(name))
	loaded, err := mockimap.LoadFixture(path)
	if err != nil {
		return err
	}
	*target = *loaded
	return nil
}
