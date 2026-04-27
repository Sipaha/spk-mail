package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	mimep "github.com/spk/spk-mail/internal/mime"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// FlagOp is the API-side representation of a flag mutation request submitted
// to the sync engine. The concrete sync.FlagOp value is constructed by the
// engine adapter; using a local type here avoids an import cycle between
// internal/api and internal/sync (sync depends on api.Emitter).
type FlagOp struct {
	AccountID int64
	FolderID  int64
	UID       int64
	Add       bool
	Flags     []string
}

// FlagOpSubmitter is implemented by the per-account worker. The engine adapter
// returns one for a given account ID.
type FlagOpSubmitter interface {
	SubmitFlagOp(op FlagOp)
}

// Engine is the minimal surface the API stub needs from the sync engine.
// internal/sync.Engine satisfies this via a tiny adapter wired in main.go.
type Engine interface {
	StartAccount(ctx context.Context, id int64)
	StopAccount(id int64)
	WorkerFor(id int64) FlagOpSubmitter
}

// Stub is the API impl: talks directly to storage/secrets and dispatches to the
// sync engine when present (production wires one; unit tests pass nil).
type Stub struct {
	Store   *storage.Store
	Secrets *secrets.Store
	Emitter *Emitter
	Engine  Engine
}

func NewStub(s *storage.Store, sec *secrets.Store, em *Emitter, eng Engine) *Stub {
	return &Stub{Store: s, Secrets: sec, Emitter: em, Engine: eng}
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
	if s.Engine != nil {
		s.Engine.StartAccount(ctx, id)
	}
	return AccountDTO{ID: id, Name: req.Name, Email: req.Email, Color: req.Color, Status: "ok"}, nil
}

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

func (s *Stub) GetThread(ctx context.Context, id int64) ([]MessageDTO, error) {
	rows, err := s.Store.GetMessagesByThread(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]MessageDTO, 0, len(rows))
	for _, r := range rows {
		var to []string
		if r.ToAddrs != nil {
			_ = json.Unmarshal([]byte(*r.ToAddrs), &to)
		}
		var fl []string
		_ = json.Unmarshal([]byte(r.Flags), &fl)
		atts, _ := s.Store.ListAttachmentsByMessage(ctx, r.ID)
		dtoAtts := make([]AttachmentDTO, 0, len(atts))
		for _, a := range atts {
			dtoAtts = append(dtoAtts, AttachmentDTO{ID: a.ID, Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.SizeBytes, Downloaded: a.LocalPath != nil})
		}
		out = append(out, MessageDTO{
			ID: r.ID, AccountID: r.AccountID, FolderID: r.FolderID,
			Subject: strFrom(r.Subject), FromAddr: strFrom(r.FromAddr), ToAddrs: to,
			Date: r.Date, Flags: fl,
			BodyText: strFrom(r.BodyText), BodyHTML: strFrom(r.BodyHTML),
			Attachments: dtoAtts,
		})
	}
	return out, nil
}

func (s *Stub) MarkRead(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		m, err := s.Store.GetMessage(ctx, id)
		if err != nil {
			return err
		}
		var fl []string
		_ = json.Unmarshal([]byte(m.Flags), &fl)
		if !contains(fl, `\Seen`) {
			fl = append(fl, `\Seen`)
		}
		b, _ := json.Marshal(fl)
		if err := s.Store.UpdateFlags(ctx, id, string(b)); err != nil {
			return err
		}
		if s.Engine != nil {
			if w := s.Engine.WorkerFor(m.AccountID); w != nil {
				w.SubmitFlagOp(FlagOp{
					AccountID: m.AccountID,
					FolderID:  m.FolderID,
					UID:       m.UID,
					Add:       true,
					Flags:     []string{`\Seen`},
				})
			}
		}
		if err := s.Store.UpdateThreadStats(ctx, ptrOr(m.ThreadID)); err != nil {
			return err
		}
		s.Emitter.Emit(Event{Type: "MessageUpdated", Payload: map[string]any{"id": id}})
	}
	return nil
}

func (s *Stub) AllowRemoteForMessage(ctx context.Context, id int64) (string, error) {
	m, err := s.Store.GetMessage(ctx, id)
	if err != nil {
		return "", err
	}
	if m.BodyHTML == nil {
		return "", nil
	}
	updated := mimep.UnblockRemote(*m.BodyHTML)
	if _, err := s.Store.DB().ExecContext(ctx, `UPDATE messages SET body_html = ? WHERE id = ?`, updated, id); err != nil {
		return "", err
	}
	s.Emitter.Emit(Event{Type: "MessageUpdated", Payload: map[string]any{"id": id}})
	return updated, nil
}

func (s *Stub) Search(ctx context.Context, query string, limit, offset int) ([]SearchHitDTO, error) {
	hits, err := s.Store.Search(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, SearchHitDTO{
			MessageID: h.MessageID,
			ThreadID:  h.ThreadID,
			Subject:   h.Subject,
			FromAddr:  h.FromAddr,
			Date:      h.Date,
			Snippet:   h.Snippet,
		})
	}
	return out, nil
}

func (s *Stub) UnreadCounts(ctx context.Context) (UnreadCountsDTO, error) {
	rows, err := s.Store.DB().QueryContext(ctx, `
		SELECT m.account_id, COUNT(*)
		FROM messages m
		JOIN folders f ON m.folder_id = f.id
		WHERE f.role = 'inbox' AND m.flags NOT LIKE '%\Seen%'
		GROUP BY m.account_id`)
	if err != nil {
		return UnreadCountsDTO{}, err
	}
	defer rows.Close()
	out := UnreadCountsDTO{PerAccount: map[int64]int64{}}
	for rows.Next() {
		var id, n int64
		if err := rows.Scan(&id, &n); err != nil {
			return UnreadCountsDTO{}, err
		}
		out.PerAccount[id] = n
		out.Total += n
	}
	return out, rows.Err()
}

func strFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrOr(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// helper that other packages use to JSON-encode flags consistently
func encodeJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
