package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	mimep "github.com/spk/spk-mail/internal/mime"
	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
)

// ErrAttachmentNotReady is returned by GetAttachmentLocalPath when the
// attachment has not yet been downloaded (or its local file disappeared).
// Callers should trigger a re-download and retry.
var ErrAttachmentNotReady = errors.New("attachment not yet downloaded")

// ErrProfileInUse is the api-side sentinel returned by DeleteProfile when the
// underlying storage layer reports that accounts are still attached to the
// profile. Wraps storage.ErrProfileInUse so callers can errors.Is-detect either.
var ErrProfileInUse = errors.New("api: profile has attached accounts")

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
		out = append(out, AccountDTO{
			ID: r.ID, Name: r.Name, Email: r.Email, Color: r.Color, Status: "ok",
			ProfileID: r.ProfileID,
		})
	}
	return out, nil
}

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
	// "starting" reflects the actual lifecycle: AccountWorker has been
	// dispatched but has not yet emitted AccountStatus{state:"connecting"} or
	// "ok". The frontend transitions through these states as events arrive.
	return AccountDTO{
		ID: id, Name: req.Name, Email: req.Email, Color: req.Color,
		Status: "starting", ProfileID: req.ProfileID,
	}, nil
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

func (s *Stub) ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadDTO, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.Store.ListThreadsByProfile(ctx, filter.ProfileID, limit, filter.Offset)
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
			if err := json.Unmarshal([]byte(*r.ToAddrs), &to); err != nil {
				slog.Warn("GetThread: bad to_addrs JSON", "id", r.ID, "err", err)
			}
		}
		var fl []string
		if err := json.Unmarshal([]byte(r.Flags), &fl); err != nil {
			slog.Warn("GetThread: bad flags JSON", "id", r.ID, "err", err)
		}
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
		if err := json.Unmarshal([]byte(m.Flags), &fl); err != nil {
			slog.Warn("MarkRead: bad flags JSON", "id", id, "err", err)
			continue
		}
		// Idempotency: skip the DB update + IMAP STORE + SSE event if the
		// message is already \Seen. Saves a round-trip and avoids spurious
		// MessageUpdated events on repeated marks.
		if contains(fl, `\Seen`) {
			continue
		}
		fl = append(fl, `\Seen`)
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
			} else {
				slog.Warn("MarkRead: no worker for account", "account_id", m.AccountID)
			}
		}
		if m.ThreadID != nil {
			if err := s.Store.UpdateThreadStats(ctx, *m.ThreadID); err != nil {
				return err
			}
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
	if err := s.Store.UpdateBodyHTML(ctx, id, updated); err != nil {
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
	total, per, err := s.Store.UnreadCountsByAccount(ctx)
	if err != nil {
		return UnreadCountsDTO{}, err
	}
	if per == nil {
		per = map[int64]int64{}
	}
	return UnreadCountsDTO{Total: total, PerAccount: per}, nil
}

// GetAttachmentLocalPath returns the local filesystem path for an attachment
// that's already been downloaded. If the row has no local_path or the file is
// missing, it returns ErrAttachmentNotReady (after clearing a stale path so
// the downloader will re-fetch).
func (s *Stub) GetAttachmentLocalPath(ctx context.Context, id int64) (string, error) {
	path, found, err := s.Store.GetAttachmentLocalPath(ctx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", ErrAttachmentNotReady
	}
	if _, err := os.Stat(path); err != nil {
		// File missing — clear so the downloader will re-fetch.
		_ = s.Store.ClearAttachmentLocalPath(ctx, id)
		return "", ErrAttachmentNotReady
	}
	return path, nil
}

// OpenAttachment hands the local file off to xdg-open (Linux). Detached: we
// don't wait for the opener to finish. Uses context.Background() for the exec
// so that a short-lived API request ctx doesn't SIGKILL xdg-open before it
// forks the real viewer.
func (s *Stub) OpenAttachment(ctx context.Context, id int64) error {
	path, err := s.GetAttachmentLocalPath(ctx, id)
	if err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", path)
	return cmd.Start()
}

func (s *Stub) ListProfiles(ctx context.Context) ([]ProfileDTO, error) {
	rows, err := s.Store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProfileDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ProfileDTO{ID: r.ID, Name: r.Name, Color: r.Color, SortOrder: r.SortOrder, Muted: r.Muted})
	}
	return out, nil
}

func (s *Stub) AddProfile(ctx context.Context, req AddProfileRequest) (ProfileDTO, error) {
	id, err := s.Store.InsertProfile(ctx, storage.ProfileRow{
		Name: req.Name, Color: req.Color, SortOrder: 0,
		CreatedAt: time.Now().Unix(),
	})
	if err != nil {
		return ProfileDTO{}, err
	}
	row, err := s.Store.GetProfile(ctx, id)
	if err != nil {
		return ProfileDTO{}, err
	}
	return ProfileDTO{ID: row.ID, Name: row.Name, Color: row.Color, SortOrder: row.SortOrder, Muted: row.Muted}, nil
}

func (s *Stub) UpdateProfile(ctx context.Context, req UpdateProfileRequest) (ProfileDTO, error) {
	if err := s.Store.UpdateProfile(ctx, req.ID, req.Name, req.Color); err != nil {
		return ProfileDTO{}, err
	}
	row, err := s.Store.GetProfile(ctx, req.ID)
	if err != nil {
		return ProfileDTO{}, err
	}
	return ProfileDTO{ID: row.ID, Name: row.Name, Color: row.Color, SortOrder: row.SortOrder, Muted: row.Muted}, nil
}

func (s *Stub) DeleteProfile(ctx context.Context, id int64) error {
	if err := s.Store.DeleteProfile(ctx, id); err != nil {
		if errors.Is(err, storage.ErrProfileInUse) {
			return fmt.Errorf("%w (profile %d)", ErrProfileInUse, id)
		}
		return err
	}
	return nil
}

func (s *Stub) SetProfileMuted(ctx context.Context, id int64, muted bool) error {
	return s.Store.SetProfileMuted(ctx, id, muted)
}

func (s *Stub) AccountIsMuted(ctx context.Context, id int64) (bool, error) {
	return s.Store.AccountIsMuted(ctx, id)
}

func (s *Stub) TotalUnreadExcludingMuted(ctx context.Context) (int64, error) {
	return s.Store.TotalUnreadExcludingMuted(ctx)
}

func strFrom(p *string) string {
	if p == nil {
		return ""
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
