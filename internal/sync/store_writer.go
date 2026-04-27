package sync

import (
	"context"
	"encoding/json"
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

func (w *StoreWriter) Submit(m IncomingMessage) { w.in <- m }
func (w *StoreWriter) Close()                   { close(w.in) }

func (w *StoreWriter) Run(ctx context.Context) {
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

// TODO: wrap thread+message+attachment inserts in a single transaction so a
// mid-flight failure doesn't leave an orphan threads row. Requires tx-aware
// Store.* method variants; deferred to a future change.
func (w *StoreWriter) process(ctx context.Context, m IncomingMessage) error {
	parsed, err := mimep.Parse(m.Raw)
	if err != nil {
		return err
	}

	bodyHTML := mimep.Sanitize(parsed.BodyHTML)

	// thread resolution
	candidates := thread.CandidateMessageIDs(parsed.InReplyTo, parsed.References)
	var threadID int64
	if len(candidates) > 0 {
		if id, ok, _ := w.store.FindThreadByMessageIDs(ctx, candidates); ok {
			threadID = id
		}
	}
	if threadID == 0 {
		// Discard real DB errors here — falling through to "create new thread"
		// is the right behavior either way; a noisy log would just spam if the
		// DB is broken (the next InsertThread call surfaces it properly).
		if id, ok, _ := w.store.FindThreadBySubject(ctx, thread.NormalizeSubject(parsed.Subject), parsed.Date.Unix(), 14*86400); ok {
			threadID = id
		}
	}
	if threadID == 0 {
		newID, err := w.store.InsertThread(ctx, storage.ThreadRow{
			SubjectNorm: thread.NormalizeSubject(parsed.Subject),
			LastDate:    parsed.Date.Unix(),
			MsgCount:    0,
		})
		if err != nil {
			return err
		}
		threadID = newID
	}

	flagsJSON, _ := json.Marshal(m.Flags)
	toJSON, _ := json.Marshal(parsed.To)
	ccJSON, _ := json.Marshal(parsed.Cc)
	refsJoined := strings.Join(parsed.References, " ")

	msgID, err := w.store.InsertMessage(ctx, storage.MessageRow{
		AccountID:      m.AccountID,
		FolderID:       m.FolderID,
		UID:            m.UID,
		MessageID:      nilIfEmpty(parsed.MessageID),
		InReplyTo:      nilIfEmpty(parsed.InReplyTo),
		References:     nilIfEmpty(refsJoined),
		ThreadID:       &threadID,
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
	})
	if err != nil {
		return err
	}

	for _, a := range parsed.Attachments {
		if _, err := w.store.InsertAttachment(ctx, storage.AttachmentRow{
			MessageID: msgID, PartID: a.PartID,
			Filename: a.Filename, ContentType: a.ContentType, SizeBytes: a.Size,
		}); err != nil {
			api.Emit(w.em, "WriteError", map[string]any{
				"err": err.Error(), "uid": m.UID, "folder_id": m.FolderID, "phase": "attachment",
			})
		}
	}

	if err := w.store.UpdateThreadStats(ctx, threadID); err != nil {
		return err
	}

	w.em.Emit(api.Event{Type: "MessageInserted", Payload: map[string]any{
		"id": msgID, "thread_id": threadID, "account_id": m.AccountID, "folder_id": m.FolderID,
	}})
	if !m.IsResync && m.FolderRole == "inbox" && !contains(m.Flags, `\Seen`) {
		w.em.Emit(api.Event{Type: "MessageArrived", Payload: map[string]any{
			"id": msgID, "thread_id": threadID, "account_id": m.AccountID,
			"subject": parsed.Subject, "from": parsed.From,
		}})
	}
	return nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
