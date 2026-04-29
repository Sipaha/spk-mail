package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/spk/spk-mail/internal/secrets"
	"github.com/spk/spk-mail/internal/storage"
	"github.com/stretchr/testify/require"
)

// newSpyStub builds a Stub backed by a real on-disk *storage.Store wrapped in
// a countingStore so the test can assert call shape (one ListAttachmentsByMessages,
// one MarkMessagesRead) at the storage seam.
func newSpyStub(t *testing.T) (*Stub, *countingStore, *storage.Store, *spyEngine) {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	cs := &countingStore{Writer: s}
	eng := &spyEngine{worker: &spyWorker{}}
	return NewStub(cs, sec, NewEmitter(), eng), cs, s, eng
}

// TestGetThread_BatchAttachments proves Stub.GetThread issues exactly one
// ListAttachmentsByMessages regardless of thread size — i.e. the N+1 the
// split-connections plan eliminated has not regressed. Three messages with
// attachments scattered across them; counter must read 1 after the call.
func TestGetThread_BatchAttachments(t *testing.T) {
	stub, cs, raw, _ := newSpyStub(t)
	ctx := context.Background()

	accID, err := raw.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)

	threadID, err := raw.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 100})
	require.NoError(t, err)
	tid := threadID
	mkMsg := func(uid int64) int64 {
		id, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid,
			ThreadID: &tid, Flags: "[]",
		})
		require.NoError(t, err)
		return id
	}
	m1, m2, m3 := mkMsg(1), mkMsg(2), mkMsg(3)
	_, err = raw.InsertAttachment(ctx, storage.AttachmentRow{
		MessageID: m1, PartID: "1.1", Filename: "a.txt",
		ContentType: "application/octet-stream", SizeBytes: 1,
	})
	require.NoError(t, err)
	_, err = raw.InsertAttachment(ctx, storage.AttachmentRow{
		MessageID: m3, PartID: "3.1", Filename: "c.txt",
		ContentType: "application/octet-stream", SizeBytes: 1,
	})
	require.NoError(t, err)
	_ = m2 // m2 has no attachments — verifies map-key-absent path stays nil-safe

	dtos, err := stub.GetThread(ctx, threadID)
	require.NoError(t, err)
	require.Len(t, dtos, 3)

	require.Equal(t, int64(1), cs.listAttsCalls.Load(),
		"GetThread must batch attachments into ONE storage call regardless of thread size")
}

// TestMarkRead_BatchTx proves Stub.MarkRead delegates to a single
// MarkMessagesRead call (not a per-id loop), emits one MessageUpdated SSE
// event per changed message, and submits one IMAP STORE op per change.
func TestMarkRead_BatchTx(t *testing.T) {
	stub, cs, raw, eng := newSpyStub(t)
	ctx := context.Background()

	accID, err := raw.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	require.NoError(t, err)
	folderID, err := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	require.NoError(t, err)
	threadID, err := raw.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 100})
	require.NoError(t, err)
	tid := threadID

	mkMsg := func(uid int64, flags string) int64 {
		id, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: uid,
			ThreadID: &tid, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, `[]`)
	m2 := mkMsg(2, `[]`)
	m3 := mkMsg(3, `["\\Seen"]`) // already seen — must NOT trigger STORE or SSE

	// Subscribe to events BEFORE the call so the handler captures everything.
	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	require.NoError(t, stub.MarkRead(ctx, []int64{m1, m2, m3}))

	require.Equal(t, int64(1), cs.markReadCalls.Load(),
		"MarkRead must collapse the per-id loop into ONE storage call")

	// Drain emitted events. Subscribe is bounded (cap 64); MarkRead emits
	// synchronously before returning, so all events are already in the channel.
	var events []Event
	for {
		select {
		case ev := <-evCh:
			events = append(events, ev)
		default:
			goto done
		}
	}
done:
	gotIDs := make([]int64, 0, len(events))
	for _, ev := range events {
		require.Equal(t, "MessageUpdated", ev.Type)
		id, ok := ev.Payload["id"].(int64)
		require.True(t, ok, "payload.id must be int64; got %T", ev.Payload["id"])
		gotIDs = append(gotIDs, id)
	}
	require.ElementsMatch(t, []int64{m1, m2}, gotIDs,
		"one MessageUpdated event per FLIPPED message (m3 was already \\Seen — no event)")

	require.Len(t, eng.worker.ops, 2, "one IMAP STORE op per flipped message")
	storeUIDs := []int64{eng.worker.ops[0].UIDs[0], eng.worker.ops[1].UIDs[0]}
	require.ElementsMatch(t, []int64{1, 2}, storeUIDs)
	for _, op := range eng.worker.ops {
		require.True(t, op.Add)
		require.Equal(t, []string{`\Seen`}, op.Flags)
		require.Equal(t, accID, op.AccountID)
		require.Equal(t, folderID, op.FolderID)
		require.Len(t, op.UIDs, 1, "per-message MarkRead must emit one Op per message with a 1-element UIDs slice")
	}
}
