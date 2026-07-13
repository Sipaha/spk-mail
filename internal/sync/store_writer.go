package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/spk/spk-mail/internal/api"
	"github.com/spk/spk-mail/internal/fsutil"
	mimep "github.com/spk/spk-mail/internal/mime"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/spk/spk-mail/internal/thread"
)

// rawRetention is the sliding window after which captured raw RFC822
// bytes get dropped by the periodic sweep. Sync-time capture skips
// messages older than this; lazy-fetch always captures (the message
// gets ~rawRetention from the click, regardless of date).
const rawRetention = 30 * 24 * time.Hour

type StoreWriter struct {
	store   storage.Writer
	em      *api.Emitter
	in      chan IncomingMessage
	dataDir string // root of the on-disk blob store; "" disables raw capture
}

func NewStoreWriter(s storage.Writer, em *api.Emitter, dataDir string) *StoreWriter {
	return &StoreWriter{store: s, em: em, in: make(chan IncomingMessage, 256), dataDir: dataDir}
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

// writerDrainGrace bounds how long Run keeps draining queued messages after
// ctx cancellation so in-flight bulk-sync batches can land before shutdown.
const writerDrainGrace = 5 * time.Second

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
			w.drainOnShutdown(nil)
			return
		case m, ok := <-w.in:
			if !ok {
				return
			}
			if ctx.Err() != nil {
				// Shutdown raced this receive: when both cases are ready
				// `select` picks at random, so we can land here with a
				// cancelled ctx. Processing m with it would fail the DB write
				// and lose the message — hand it to the drain path instead,
				// which writes on a fresh ctx.
				w.drainOnShutdown(&m)
				return
			}
			if err := w.process(ctx, m); err != nil {
				api.Emit(w.em, "WriteError", map[string]any{"err": err.Error(), "uid": m.UID, "folder_id": m.FolderID})
			}
		}
	}
}

// drainOnShutdown processes messages already queued when ctx cancelled, for up
// to writerDrainGrace, on a fresh context (the run ctx is dead — writing on it
// would fail). `pending`, when non-nil, is a message already taken off the
// queue by Run and is written first so it isn't lost. drainOnShutdown returns
// as soon as the queue is empty — nothing closes w.in, so blocking on a receive
// would stall every shutdown by the full grace period. Anything still queued at
// the deadline is logged and dropped.
func (w *StoreWriter) drainOnShutdown(pending *IncomingMessage) {
	drainCtx, cancel := context.WithTimeout(context.Background(), writerDrainGrace)
	defer cancel()
	deadline := time.After(writerDrainGrace)
	if pending != nil {
		if err := w.process(drainCtx, *pending); err != nil {
			api.Emit(w.em, "WriteError", map[string]any{"err": err.Error(), "uid": pending.UID, "folder_id": pending.FolderID})
		}
	}
	for {
		select {
		case m, ok := <-w.in:
			if !ok {
				return
			}
			if err := w.process(drainCtx, m); err != nil {
				api.Emit(w.em, "WriteError", map[string]any{"err": err.Error(), "uid": m.UID, "folder_id": m.FolderID})
			}
		case <-deadline:
			if n := len(w.in); n > 0 {
				slog.Warn("store writer shutdown: dropped queued messages", "count", n)
			}
			return
		default:
			return
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

	// json.Marshal of a nil []string emits "null" — round-tripped via the
	// frontend that becomes JS null, and `null.includes('\Seen')` blows up
	// the open-thread mark-read path. Force an empty array for nil/empty so
	// the frontend always sees an iterable list.
	flagsSrc := m.Flags
	if flagsSrc == nil {
		flagsSrc = []string{}
	}
	flagsJSON, _ := json.Marshal(flagsSrc)
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
	if w.dataDir != "" && parsed.Date.After(time.Now().Add(-rawRetention)) {
		w.captureRaw(ctx, msgID, m.Raw)
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// captureRaw mirrors raw RFC822 bytes into the blob store and links
// them via messages.raw_blob_id. Best-effort: failures are logged via
// slog.Warn and otherwise swallowed, since the parsed-message tx is
// already committed and the lazy-fetch path covers any gap.
func (w *StoreWriter) captureRaw(ctx context.Context, msgID int64, raw []byte) {
	sha, size, err := fsutil.WriteContentAddressed(bytes.NewReader(raw), func(hex string) string {
		return storage.BlobPath(w.dataDir, hex)
	})
	if err != nil {
		slog.Warn("raw capture: write blob", "msg_id", msgID, "err", err)
		return
	}
	blobID, _, err := w.store.InsertOrIncBlob(ctx, sha, size, time.Now().Unix())
	if err != nil {
		slog.Warn("raw capture: InsertOrIncBlob", "msg_id", msgID, "err", err)
		return
	}
	res, prev, err := w.store.SetMessageRawBlob(ctx, msgID, blobID, time.Now().Unix())
	if err != nil {
		slog.Warn("raw capture: SetMessageRawBlob", "msg_id", msgID, "err", err)
		_, _ = w.store.DecBlobRef(ctx, blobID)
		return
	}
	switch res {
	case storage.SetReplaced:
		_, _ = w.store.DecBlobRef(ctx, prev)
	case storage.SetNoop:
		_, _ = w.store.DecBlobRef(ctx, blobID)
	}
}
