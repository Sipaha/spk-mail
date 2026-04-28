package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/spk/spk-mail/internal/api"
	mimep "github.com/spk/spk-mail/internal/mime"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/spk/spk-mail/internal/thread"
)

type StoreWriter struct {
	store *storage.Store
	em    *api.Emitter
	in    chan IncomingMessage
}

func NewStoreWriter(s *storage.Store, em *api.Emitter) *StoreWriter {
	return &StoreWriter{store: s, em: em, in: make(chan IncomingMessage, 256)}
}

// Submit queues a parsed message for writing. Returns ctx.Err() if the
// caller's context cancels while the writer's bounded queue is full —
// without this, an AccountWorker mid-bulk-fetch would deadlock here on
// engine shutdown when the writer goroutine has already stopped consuming.
func (w *StoreWriter) Submit(ctx context.Context, m IncomingMessage) error {
	select {
	case w.in <- m:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *StoreWriter) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			// Don't take the whole engine down on a writer panic — log it
			// and let the worker error path surface as a WriteError.
			api.Emit(w.em, "WriteError", map[string]any{"err": fmt.Sprintf("writer panic: %v", r)})
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-w.in:
			if !ok {
				return
			}
			if err := w.process(ctx, m); err != nil {
				api.Emit(w.em, "WriteError", map[string]any{"err": err.Error(), "uid": m.UID, "folder_id": m.FolderID})
			}
		}
	}
}

func (w *StoreWriter) process(ctx context.Context, m IncomingMessage) error {
	if m.Ack != nil {
		defer m.Ack()
	}
	parsed, err := mimep.Parse(m.Raw)
	if err != nil {
		return err
	}

	bodyHTML := mimep.Sanitize(parsed.BodyHTML)

	// Thread resolution runs outside the write tx — these are read-only
	// lookups against committed state, and they may legitimately return
	// "no match" without preventing a new thread from being created
	// inside the bundle below.
	candidates := thread.CandidateMessageIDs(parsed.InReplyTo, parsed.References)
	var existingThreadID int64
	if len(candidates) > 0 {
		// Surface transient busy-timeout / disk errors as a warn so a
		// silent split-into-singleton (reply landing in its own thread
		// because the lookup failed, not because nothing matched) shows
		// up in the testapi log buffer. Fallthrough to subject + bundle
		// insert is unchanged — this is observation, not behavior.
		id, ok, err := w.store.FindThreadByMessageIDs(ctx, candidates)
		if err != nil {
			slog.Warn("FindThreadByMessageIDs failed; falling through to subject match",
				"account_id", m.AccountID, "uid", m.UID, "err", err)
		}
		if ok {
			existingThreadID = id
		}
	}
	if existingThreadID == 0 {
		// As above, log warn but keep going — InsertParsedMessageBundle below
		// will surface a real DB outage as a WriteError event regardless.
		id, ok, err := w.store.FindThreadBySubject(ctx, m.AccountID, thread.NormalizeSubject(parsed.Subject), parsed.Date.Unix(), 14*86400)
		if err != nil {
			slog.Warn("FindThreadBySubject failed; will create a new thread",
				"account_id", m.AccountID, "uid", m.UID, "err", err)
		}
		if ok {
			existingThreadID = id
		}
	}

	flagsJSON, _ := json.Marshal(m.Flags)
	toJSON, _ := json.Marshal(parsed.To)
	ccJSON, _ := json.Marshal(parsed.Cc)
	refsJoined := strings.Join(parsed.References, " ")

	atts := make([]storage.AttachmentRow, 0, len(parsed.Attachments))
	for _, a := range parsed.Attachments {
		atts = append(atts, storage.AttachmentRow{
			PartID: a.PartID, Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.Size,
		})
	}

	// Single tx for thread+message+attachments+stats. If any step fails, the
	// rollback leaves no orphan rows and the caller can retry the bundle.
	msgID, threadID, err := w.store.InsertParsedMessageBundle(ctx, storage.MessageBundle{
		ExistingThreadID: existingThreadID,
		NewThread: storage.ThreadRow{
			SubjectNorm: thread.NormalizeSubject(parsed.Subject),
			LastDate:    parsed.Date.Unix(),
			MsgCount:    0,
		},
		Message: storage.MessageRow{
			AccountID:      m.AccountID,
			FolderID:       m.FolderID,
			UID:            m.UID,
			MessageID:      nilIfEmpty(parsed.MessageID),
			InReplyTo:      nilIfEmpty(parsed.InReplyTo),
			References:     nilIfEmpty(refsJoined),
			Subject:        nilIfEmpty(parsed.Subject),
			FromAddr:       nilIfEmpty(parsed.From),
			ToAddrs:        nilIfEmpty(string(toJSON)),
			CcAddrs:        nilIfEmpty(string(ccJSON)),
			Date:           parsed.Date.Unix(),
			Flags:          string(flagsJSON),
			HasAttachments: len(parsed.Attachments) > 0,
			SizeBytes:      int64(len(m.Raw)),
			BodyText:       nilIfEmpty(parsed.BodyText),
			BodyHTML:       nilIfEmpty(bodyHTML),
		},
		Attachments: atts,
	})
	if err != nil {
		return err
	}

	w.em.Emit(api.Event{Type: "MessageInserted", Payload: map[string]any{
		"id": msgID, "thread_id": threadID, "account_id": m.AccountID, "folder_id": m.FolderID,
	}})
	if !m.IsResync && m.FolderRole == "inbox" && !slices.Contains(m.Flags, `\Seen`) {
		w.em.Emit(api.Event{Type: "MessageArrived", Payload: map[string]any{
			"id": msgID, "thread_id": threadID, "account_id": m.AccountID,
			"subject": parsed.Subject, "from": parsed.From,
		}})
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
