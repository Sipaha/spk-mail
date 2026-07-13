package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/spk/spk-mail/internal/events"
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

// prettyFolderName converts the raw IMAP mailbox name into a display-friendly
// label: ALL-CAPS segments become Title Case (so "INBOX" → "Inbox", "GMAIL"
// → "Gmail"), while names that already use mixed case ("Spam", "[Gmail]/All
// Mail") are left untouched. The hierarchy delimiter is preserved so nested
// folders like "Drafts|template" still split into "Drafts|Template".
//
// We only normalize a segment when it is ENTIRELY upper-case ASCII letters /
// digits — that's the only form we're confident is a mechanical convention
// (the IMAP RFC special-cases "INBOX" as case-insensitive; everything else
// is a server-defined label). User-named all-caps folders ("WORK", "TODO")
// will get title-cased too — that's an intentional cosmetic improvement,
// not a regression.
func prettyFolderName(name, delim string) string {
	if name == "" {
		return name
	}
	d := delim
	if d == "" {
		// No declared delimiter — most common with single-segment names.
		return titleSegment(name)
	}
	parts := splitDelim(name, d)
	for i, p := range parts {
		parts[i] = titleSegment(p)
	}
	return joinDelim(parts, d)
}

func splitDelim(s, d string) []string {
	if d == "" || len(d) > 1 {
		return []string{s}
	}
	out := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == d[0] {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func joinDelim(parts []string, d string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += d + p
	}
	return out
}

func titleSegment(s string) string {
	if s == "" {
		return s
	}
	// Detect "all upper-case ASCII letters" — leave anything with a lower-case
	// letter or non-ASCII alone (Russian / German / mixed-case names).
	allUpper := false
	hasLetter := false
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			allUpper = true
			hasLetter = true
		} else if r >= 'a' && r <= 'z' {
			allUpper = false
			hasLetter = true
			break
		}
	}
	if !hasLetter || !allUpper {
		return s
	}
	out := make([]byte, 0, len(s))
	prevAlpha := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if c >= 'A' && c <= 'Z' && prevAlpha {
			out = append(out, c+('a'-'A'))
		} else {
			out = append(out, c)
		}
		prevAlpha = isAlpha
	}
	return string(out)
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
			Name: prettyFolderName(r.Name, r.Delimiter), Role: role,
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
			AccountID: r.AccountID,
			LastFrom:  strFrom(r.LastFrom),
			Snippet:   collapseWhitespace(strFrom(r.Snippet)),
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

	// Build the cross-message remote-image allowlist once per call. URLs
	// the user has approved on any past message auto-unblock here without
	// touching the stored body_html — the persisted column keeps its
	// data-spk-original-src markers so reverting an approval (a future
	// feature) wouldn't have to reconstruct them.
	approved, err := s.Store.ListApprovedRemoteURLs(ctx)
	if err != nil {
		slog.Warn("GetThread: ListApprovedRemoteURLs failed; rendering with no allowlist", "err", err)
		approved = nil
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
			// "Downloaded" must reflect either the legacy local_path column
			// OR the new content-addressed blob_id. AttachmentDownloader
			// intentionally clears local_path when it points the row at a
			// blob (see UpdateAttachmentDownloaded), so checking only
			// local_path leaves new downloads stuck reporting downloaded=false.
			dtoAtts = append(dtoAtts, AttachmentDTO{
				ID: a.ID, Filename: a.Filename, ContentType: a.ContentType,
				SizeBytes: a.SizeBytes, Downloaded: a.LocalPath != nil || a.BlobID != nil,
			})
		}
		bodyHTML := strFrom(r.BodyHTML)
		if bodyHTML != "" && len(approved) > 0 {
			bodyHTML = mimep.UnblockApproved(bodyHTML, approved)
		}
		out = append(out, MessageDTO{
			ID: r.ID, AccountID: r.AccountID, FolderID: r.FolderID,
			Subject: strFrom(r.Subject), FromAddr: strFrom(r.FromAddr), ToAddrs: to,
			Date: r.Date, Flags: fl,
			BodyText: strFrom(r.BodyText), BodyHTML: bodyHTML,
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
	// One bulk Op per (account, folder) — never one per message. SubmitFlagOp
	// blocks while a worker's queue is full (worker down, supervise backing
	// off), so a per-message fan-out would stall this handler by N × the
	// enqueue timeout on an IMAP outage. Grouping mirrors ToggleThreadFlagged:
	// UIDs are folder-scoped, so folders cannot share an Op.
	groups := make(map[folderKey][]int64)
	for _, ch := range out.Changed {
		k := folderKey{ch.AccountID, ch.FolderID}
		groups[k] = append(groups[k], ch.UID)
	}
	for k, uids := range groups {
		s.submitFlagOp(ctx, "MarkRead", k, uids, true, `\Seen`)
	}
	for _, ch := range out.Changed {
		s.Emitter.Emit(events.Event{Type: "MessageUpdated", Payload: map[string]any{
			"id":         ch.MessageID,
			"account_id": ch.AccountID,
			"folder_id":  ch.FolderID,
		}})
	}
	return nil
}

// folderKey identifies the IMAP mailbox a bulk flag op addresses. A thread can
// span folders if a message moved, and IMAP UIDs are folder-scoped, so ops are
// always grouped per (account, folder) — one covering both would address the
// wrong messages.
type folderKey struct {
	accountID int64
	folderID  int64
}

// submitFlagOp hands one bulk op to the account's worker, logging (never
// failing the request) when there is no worker or the queue stays full: the
// DB is already updated, and the worker re-syncs flags from the server on its
// next pass, so a dropped op degrades to a delayed IMAP flag, not lost state.
func (s *Stub) submitFlagOp(ctx context.Context, caller string, k folderKey, uids []int64, add bool, flag string) {
	if s.Engine == nil {
		return
	}
	w := s.Engine.WorkerFor(k.accountID)
	if w == nil {
		slog.Warn(caller+": no worker for account",
			"account_id", k.accountID, "folder_id", k.folderID)
		return
	}
	if err := w.SubmitFlagOp(ctx, flagop.Op{
		AccountID: k.accountID,
		FolderID:  k.folderID,
		UIDs:      uids,
		Add:       add,
		Flags:     []string{flag},
	}); err != nil {
		slog.Warn(caller+": flag op submit failed",
			"account_id", k.accountID, "folder_id", k.folderID, "err", err)
	}
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
	s.submitFlagOp(ctx, "MarkFolderRead", folderKey{accountID, folderID}, uids, true, `\Seen`)
	s.Emitter.Emit(events.Event{
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

	groups := make(map[folderKey][]int64)
	for _, ch := range out.Changed {
		k := folderKey{ch.AccountID, ch.FolderID}
		groups[k] = append(groups[k], ch.UID)
	}
	for k, uids := range groups {
		s.submitFlagOp(ctx, "ToggleThreadFlagged", k, uids, out.Action == "added", `\Flagged`)
	}

	for _, ch := range out.Changed {
		s.Emitter.Emit(events.Event{
			Type: "MessageUpdated",
			Payload: map[string]any{
				"id":         ch.MessageID,
				"account_id": ch.AccountID,
				"folder_id":  ch.FolderID,
			},
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
	// Cache the URLs the user is approving so other messages with the same
	// remote images render inline on the next GetThread without forcing
	// another click. Done before UnblockRemote so we can still see the
	// data-spk-original-src markers.
	if urls := mimep.ExtractBlockedURLs(*m.BodyHTML); len(urls) > 0 {
		if err := s.Store.AddApprovedRemoteURLs(ctx, urls); err != nil {
			slog.Warn("AllowRemoteForMessage: AddApprovedRemoteURLs failed", "id", id, "err", err)
		}
	}
	updated := mimep.UnblockRemote(*m.BodyHTML)
	if err := s.Store.UpdateBodyHTML(ctx, id, updated); err != nil {
		return "", err
	}
	s.Emitter.Emit(events.Event{Type: "MessageUpdated", Payload: map[string]any{
		"id":         id,
		"account_id": m.AccountID,
		"folder_id":  m.FolderID,
	}})
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
