package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// Stub is the API impl used in plan 1: it talks directly to storage and secrets.
// In plan 2 it will also dispatch to the sync engine.
type Stub struct {
	Store   *storage.Store
	Secrets *secrets.Store
	Emitter *Emitter
}

func NewStub(s *storage.Store, sec *secrets.Store, em *Emitter) *Stub {
	return &Stub{Store: s, Secrets: sec, Emitter: em}
}

func (s *Stub) ListAccounts(ctx context.Context) ([]AccountDTO, error) {
	rows, err := s.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AccountDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, AccountDTO{ID: r.ID, Name: r.Name, Email: r.Email, Color: r.Color, Status: "ok"})
	}
	return out, nil
}

func (s *Stub) AddAccount(ctx context.Context, req AddAccountRequest) (AccountDTO, error) {
	id, err := s.Store.InsertAccount(ctx, storage.AccountRow{
		Name: req.Name, Email: req.Email,
		IMAPHost: req.IMAPHost, IMAPPort: req.IMAPPort, IMAPUsername: req.IMAPUsername,
		UseTLS: req.UseTLS, Color: req.Color, CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return AccountDTO{}, err
	}
	if err := s.Secrets.Set(fmt.Sprintf("account:%d", id), []byte(req.IMAPPassword)); err != nil {
		return AccountDTO{}, err
	}
	return AccountDTO{ID: id, Name: req.Name, Email: req.Email, Color: req.Color, Status: "ok"}, nil
}

func (s *Stub) RemoveAccount(ctx context.Context, id int64) error {
	if err := s.Store.DeleteAccount(ctx, id); err != nil {
		return err
	}
	_ = s.Secrets.Delete(fmt.Sprintf("account:%d", id))
	return nil
}

func (s *Stub) ListThreads(ctx context.Context, _ ThreadFilter) ([]ThreadDTO, error) {
	rows, err := s.Store.ListThreadsRecent(ctx, 100, 0)
	if err != nil {
		return nil, err
	}
	out := make([]ThreadDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ThreadDTO{ID: r.ID, Subject: r.SubjectNorm, LastDate: r.LastDate, MsgCount: r.MsgCount, UnreadCount: r.UnreadCount, HasFlagged: r.HasFlagged, HasAttach: r.HasAttach})
	}
	return out, nil
}

func (s *Stub) GetThread(ctx context.Context, _ int64) ([]MessageDTO, error) {
	// plan 2 implements; plan 1 returns empty
	return []MessageDTO{}, nil
}

func (s *Stub) MarkRead(_ context.Context, _ []int64) error { return nil }
func (s *Stub) AllowRemoteForMessage(_ context.Context, _ int64) (string, error) {
	return "", nil
}
func (s *Stub) Search(_ context.Context, _ string, _, _ int) ([]MessageDTO, error) {
	return nil, nil
}

// helper that other packages use to JSON-encode flags consistently
func encodeJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
