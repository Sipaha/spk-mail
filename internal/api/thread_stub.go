package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/spk/spk-mail/internal/flagop"
	mimep "github.com/spk/spk-mail/internal/mime"
	"github.com/spk/spk-mail/internal/storage"
)

// roleOrder ranks folder roles for the sidebar tree. Lower = earlier.
// Custom (no role) sits in the middle so user-named folders don't get
// pushed below spam/trash.
var roleOrder = map[string]int{
	"inbox":   0,
	"sent":    1,
	"drafts":  2,
	"archive": 3,
	"":        4,
	"spam":    5,
	"trash":   6,
}

func (s *Stub) ListFolders(ctx context.Context, accountID int64) ([]FolderDTO, error) {
	rows, err := s.Store.ListFolders(ctx, accountID)
	if err != nil {
		return nil, err
	}
	counts, _ := s.Store.MessageCountsByFolder(ctx, accountID)
	out := make([]FolderDTO, 0, len(rows))
	for _, r := range rows {
		role := ""
		if r.Role != nil {
			role = *r.Role
		}
		c := counts[r.ID]
		out = append(out, FolderDTO{
			ID: r.ID, AccountID: accountID,
			Name: r.Name, Role: role,
			UnreadCount:  c.Unread,
			TotalCount:   c.Total,
			FlaggedCount: c.Flagged,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := roleOrder[out[i].Role], roleOrder[out[j].Role]
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *Stub) ListThreads(ctx context.Context, filter ThreadFilter) ([]ThreadDTO, error) {
	sf := storage.ThreadFilter{
		AccountID:  filter.AccountID,
		FolderID:   filter.FolderID,
		ProfileID:  filter.ProfileID,
		UnreadOnly: filter.UnreadOnly,
		HasFlagged: filter.HasFlagged,
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.Store.ListThreads(ctx, sf, limit, filter.Offset)
	if err != nil {
		return nil, err
	}
	out := make([]ThreadDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, ThreadDTO{
			ID: r.ID, Subject: r.SubjectNorm, LastDate: r.LastDate,
			MsgCount: r.MsgCount, UnreadCount: r.UnreadCount,
			HasFlagged: r.HasFlagged, HasAttach: r.HasAttach,
			LastFrom: strFrom(r.LastFrom),
			Snippet:  collapseWhitespace(strFrom(r.Snippet)),
		})
	}
	return out, nil
}

func (s *Stub) GetThread(ctx context.Context, id int64) ([]MessageDTO, error) {
	rows, err := s.Store.GetMessagesByThread(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []MessageDTO{}, nil
	}

	msgIDs := make([]int64, len(rows))
	for i, r := range rows {
		msgIDs[i] = r.ID
	}
	attsByMsg, err := s.Store.ListAttachmentsByMessages(ctx, msgIDs)
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
		atts := attsByMsg[r.ID] // nil-slice-friendly: missing key → nil
		dtoAtts := make([]AttachmentDTO, 0, len(atts))
		for _, a := range atts {
			dtoAtts = append(dtoAtts, AttachmentDTO{
				ID: a.ID, Filename: a.Filename, ContentType: a.ContentType,
				SizeBytes: a.SizeBytes, Downloaded: a.LocalPath != nil,
			})
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
	out, err := s.Store.MarkMessagesRead(ctx, ids)
	if err != nil {
		return err
	}
	for _, ch := range out.Changed {
		if s.Engine != nil {
			if w := s.Engine.WorkerFor(ch.AccountID); w != nil {
				w.SubmitFlagOp(flagop.Op{
					AccountID: ch.AccountID,
					FolderID:  ch.FolderID,
					UIDs:      []int64{ch.UID},
					Add:       true,
					Flags:     []string{`\Seen`},
				})
			} else {
				slog.Warn("MarkRead: no worker for account", "account_id", ch.AccountID)
			}
		}
		s.Emitter.Emit(Event{Type: "MessageUpdated", Payload: map[string]any{"id": ch.MessageID}})
	}
	return nil
}

// MarkFolderRead flips \Seen on every unread message in the folder. The
// storage layer does the work in one writer transaction; the API layer fans
// out a SINGLE bulk flagop.Op (so AccountWorker issues one IMAP STORE for
// the whole set, not N) and emits ONE FolderMarkedRead SSE event so the
// frontend can refetch folder counts + open thread once instead of N times.
func (s *Stub) MarkFolderRead(ctx context.Context, folderID int64) (int64, error) {
	out, err := s.Store.MarkFolderMessagesRead(ctx, folderID)
	if err != nil {
		return 0, err
	}
	if len(out.Changed) == 0 {
		return 0, nil
	}
	accountID := out.Changed[0].AccountID
	uids := make([]int64, len(out.Changed))
	for i, ch := range out.Changed {
		uids[i] = ch.UID
	}
	if s.Engine != nil {
		if w := s.Engine.WorkerFor(accountID); w != nil {
			w.SubmitFlagOp(flagop.Op{
				AccountID: accountID,
				FolderID:  folderID,
				UIDs:      uids,
				Add:       true,
				Flags:     []string{`\Seen`},
			})
		} else {
			slog.Warn("MarkFolderRead: no worker for account",
				"account_id", accountID, "folder_id", folderID)
		}
	}
	s.Emitter.Emit(Event{
		Type: "FolderMarkedRead",
		Payload: map[string]any{
			"account_id": accountID,
			"folder_id":  folderID,
			"count":      int64(len(out.Changed)),
		},
	})
	return int64(len(out.Changed)), nil
}

// ToggleThreadFlagged orchestrates a thread-level \Flagged toggle. Storage
// runs the toggle in a single writer tx and returns per-message metadata;
// this layer fans the IMAP STORE ops out as ONE bulk flagop.Op per
// (account, folder) pair (a thread can in principle span folders) and
// emits one MessageUpdated SSE per changed message — the existing events.ts
// handler picks those up and refetches threads + open thread, so
// has_flagged on ThreadRow refreshes without a new event-type.
func (s *Stub) ToggleThreadFlagged(ctx context.Context, threadID int64) (FlagToggleResult, error) {
	out, err := s.Store.ToggleThreadFlagged(ctx, threadID)
	if err != nil {
		return FlagToggleResult{}, err
	}
	if out.Action == "noop" {
		return FlagToggleResult{Action: "noop", Count: 0}, nil
	}

	// Group changed UIDs by (account, folder) — typically one group, but a
	// thread CAN span folders if a message moved between them, and IMAP
	// UIDs are folder-scoped, so a single Op covering both would address
	// the wrong messages.
	type key struct {
		accountID int64
		folderID  int64
	}
	groups := make(map[key][]int64)
	for _, ch := range out.Changed {
		k := key{ch.AccountID, ch.FolderID}
		groups[k] = append(groups[k], ch.UID)
	}

	if s.Engine != nil {
		for k, uids := range groups {
			w := s.Engine.WorkerFor(k.accountID)
			if w == nil {
				slog.Warn("ToggleThreadFlagged: no worker for account",
					"account_id", k.accountID, "folder_id", k.folderID)
				continue
			}
			w.SubmitFlagOp(flagop.Op{
				AccountID: k.accountID,
				FolderID:  k.folderID,
				UIDs:      uids,
				Add:       out.Action == "added",
				Flags:     []string{`\Flagged`},
			})
		}
	}

	for _, ch := range out.Changed {
		s.Emitter.Emit(Event{
			Type:    "MessageUpdated",
			Payload: map[string]any{"id": ch.MessageID},
		})
	}

	return FlagToggleResult{Action: out.Action, Count: int64(len(out.Changed))}, nil
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

func strFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// collapseWhitespace folds CR/LF and runs of spaces/tabs into single spaces and
// trims the result. Used on body_text snippets so multi-line bodies render as a
// single readable preview line in the UI.
func collapseWhitespace(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // leading-trim: treat start as space so we skip leading whitespace
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' || r == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	out := b.String()
	// Trailing space (if input ended in whitespace) — strip one.
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return out
}
