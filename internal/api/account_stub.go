package api

import (
	"context"
	"fmt"
	"time"

	"github.com/spk/spk-mail/internal/storage"
)

// ListAccounts returns every account row plus a synthetic "ok" status.
// Real-time status transitions arrive on the event bus as AccountStatus
// events, so the field returned here is just the initial steady state.
func (s *Stub) ListAccounts(ctx context.Context) ([]AccountDTO, error) {
	rows, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AccountDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountDTO{
			ID: r.ID, Name: r.Name, Email: r.Email, Color: r.Color, Status: "ok",
			ProfileID: r.ProfileID,
		})
	}
	return out, nil
}

// AddAccount inserts the account row, stores the IMAP password in the
// secrets keyring, and (when an Engine is wired) kicks the AccountWorker
// off. Returns "starting" as the initial status — the worker will emit
// AccountStatus events as it transitions through "connecting" → "ok".
func (s *Stub) AddAccount(ctx context.Context, req AddAccountRequest) (AccountDTO, error) {
	id, err := s.Store.InsertAccount(ctx, storage.AccountRow{
		Name: req.Name, Email: req.Email,
		IMAPHost: req.IMAPHost, IMAPPort: req.IMAPPort, IMAPUsername: req.IMAPUsername,
		UseTLS: req.UseTLS, Color: req.Color, CreatedAt: time.Now().Unix(),
		ProfileID: req.ProfileID,
	})
	if err != nil {
		return AccountDTO{}, err
	}
	if err := s.Secrets.Set(fmt.Sprintf("account:%d", id), []byte(req.IMAPPassword)); err != nil {
		return AccountDTO{}, err
	}
	if s.Engine != nil {
		s.Engine.StartAccount(ctx, id)
	}
	return AccountDTO{
		ID: id, Name: req.Name, Email: req.Email, Color: req.Color,
		Status: "starting", ProfileID: req.ProfileID,
	}, nil
}

// RemoveAccount stops the worker, deletes the row, and best-effort wipes
// the password. Order matters: stop the worker first so it can't try to
// dial against a row that's about to disappear.
func (s *Stub) RemoveAccount(ctx context.Context, id int64) error {
	if s.Engine != nil {
		s.Engine.StopAccount(id)
	}
	if err := s.Store.DeleteAccount(ctx, id); err != nil {
		return err
	}
	_ = s.Secrets.Delete(fmt.Sprintf("account:%d", id))
	return nil
}

func (s *Stub) AccountIsMuted(ctx context.Context, id int64) (bool, error) {
	return s.Store.AccountIsMuted(ctx, id)
}

func (s *Stub) TotalUnreadExcludingMuted(ctx context.Context) (int64, error) {
	return s.Store.TotalUnreadExcludingMuted(ctx)
}
