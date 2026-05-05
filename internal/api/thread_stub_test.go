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

// TestGetThread_DownloadedReflectsBlobID is the regression sentry for the
// content-addressed-blob bug: AttachmentDownloader clears local_path and
// writes blob_id when bytes land, so the DTO mapper must treat either
// column as proof-of-download. Without the `|| a.BlobID != nil` clause
// in thread_stub.go the chip in the UI stuck on "Downloading…" forever.
func TestGetThread_DownloadedReflectsBlobID(t *testing.T) {
	stub, _, raw, _ := newSpyStub(t)
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
	msgID, err := raw.InsertMessage(ctx, storage.MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 1,
		ThreadID: &tid, Flags: "[]",
	})
	require.NoError(t, err)
	pendingID, err := raw.InsertAttachment(ctx, storage.AttachmentRow{
		MessageID: msgID, PartID: "1", Filename: "pending.bin",
		ContentType: "application/octet-stream", SizeBytes: 1,
	})
	require.NoError(t, err)
	doneID, err := raw.InsertAttachment(ctx, storage.AttachmentRow{
		MessageID: msgID, PartID: "2", Filename: "done.bin",
		ContentType: "application/octet-stream", SizeBytes: 1,
	})
	require.NoError(t, err)

	// Simulate the downloader landing bytes for `done.bin`: blob row, then
	// UpdateAttachmentDownloaded — which intentionally clears local_path
	// and points blob_id at the blob.
	blobID, _, err := raw.InsertOrIncBlob(ctx, "sha-done", 1, 0)
	require.NoError(t, err)
	require.NoError(t, raw.UpdateAttachmentDownloaded(ctx, doneID, blobID, "sha-done", 1))

	dtos, err := stub.GetThread(ctx, threadID)
	require.NoError(t, err)
	require.Len(t, dtos, 1)
	require.Len(t, dtos[0].Attachments, 2)

	got := map[int64]bool{}
	for _, a := range dtos[0].Attachments {
		got[a.ID] = a.Downloaded
	}
	require.False(t, got[pendingID], "pending attachment must report Downloaded=false")
	require.True(t, got[doneID],
		"attachment with blob_id set must report Downloaded=true even though local_path is NULL")
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

// TestMarkFolderRead_BatchTx proves Stub.MarkFolderRead delegates to a single
// MarkFolderMessagesRead storage call, submits one BULK flagop.Op (UIDs slice
// holding every flipped UID), and emits exactly one FolderMarkedRead SSE event.
func TestMarkFolderRead_BatchTx(t *testing.T) {
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
	mkMsg(1, `[]`)
	mkMsg(2, `[]`)
	mkMsg(3, `[]`)
	mkMsg(4, `[]`)
	mkMsg(5, `[]`)
	mkMsg(6, `["\\Seen"]`) // already seen
	mkMsg(7, `["\\Seen"]`) // already seen

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	count, err := stub.MarkFolderRead(ctx, folderID)
	require.NoError(t, err)
	require.Equal(t, int64(5), count)

	require.Equal(t, int64(1), cs.markFolderReadCalls.Load(),
		"MarkFolderRead must call storage exactly once")

	require.Len(t, eng.worker.ops, 1, "MarkFolderRead must submit ONE bulk flag op, not N per-message ops")
	op := eng.worker.ops[0]
	require.Equal(t, accID, op.AccountID)
	require.Equal(t, folderID, op.FolderID)
	require.True(t, op.Add)
	require.Equal(t, []string{`\Seen`}, op.Flags)
	require.Len(t, op.UIDs, 5, "bulk Op carries every flipped UID in one slice")
	require.ElementsMatch(t, []int64{1, 2, 3, 4, 5}, op.UIDs)

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
	require.Len(t, events, 1, "MarkFolderRead must emit exactly ONE FolderMarkedRead event")
	require.Equal(t, "FolderMarkedRead", events[0].Type)
	require.Equal(t, accID, events[0].Payload["account_id"])
	require.Equal(t, folderID, events[0].Payload["folder_id"])
	require.Equal(t, int64(5), events[0].Payload["count"])
}

// TestMarkFolderRead_NothingToFlip — folder with all-seen messages: storage
// is still consulted (returns empty outcome), but no flag op is submitted
// and no SSE event fires. Returns 0.
func TestMarkFolderRead_NothingToFlip(t *testing.T) {
	stub, cs, raw, eng := newSpyStub(t)
	ctx := context.Background()

	accID, _ := raw.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	for i := int64(1); i <= 3; i++ {
		_, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: i, Date: i, Flags: `["\\Seen"]`,
		})
		require.NoError(t, err)
	}

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	count, err := stub.MarkFolderRead(ctx, folderID)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
	require.Equal(t, int64(1), cs.markFolderReadCalls.Load(),
		"storage is still consulted (it returns empty outcome)")
	require.Empty(t, eng.worker.ops, "no flag op when nothing flipped")

	select {
	case ev := <-evCh:
		t.Fatalf("no SSE event expected, got %+v", ev)
	default:
		// good — no event
	}
}

// TestToggleThreadFlagged_Add — unflagged thread of 3 messages, click ★
// once. Expect: storage called once; ONE bulk flag op with the most-
// recent message's UID, Add=true, Flags=[\Flagged]; ONE MessageUpdated
// SSE event for that message; FlagToggleResult{Action:"added", Count:1}.
func TestToggleThreadFlagged_Add(t *testing.T) {
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
	threadID, err := raw.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 300})
	require.NoError(t, err)
	tid := threadID

	mkMsg := func(uid, date int64) int64 {
		id, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: `[]`,
		})
		require.NoError(t, err)
		return id
	}
	mkMsg(1, 100)
	mkMsg(2, 200)
	m3 := mkMsg(3, 300) // most recent

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	res, err := stub.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "added", res.Action)
	require.Equal(t, int64(1), res.Count)

	require.Equal(t, int64(1), cs.toggleFlaggedCalls.Load(),
		"storage must be called exactly once")
	require.Len(t, eng.worker.ops, 1,
		"exactly one bulk flag op (one folder, one Add direction)")
	op := eng.worker.ops[0]
	require.Equal(t, accID, op.AccountID)
	require.Equal(t, folderID, op.FolderID)
	require.True(t, op.Add)
	require.Equal(t, []string{`\Flagged`}, op.Flags)
	require.Equal(t, []int64{3}, op.UIDs)

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
	require.Len(t, events, 1)
	require.Equal(t, "MessageUpdated", events[0].Type)
	require.Equal(t, m3, events[0].Payload["id"])
}

// TestToggleThreadFlagged_Remove — thread with 2 flagged + 1 unflagged.
// Expect: 1 bulk op with both flagged UIDs, Add=false; 2 MessageUpdated;
// Action="removed", Count=2.
func TestToggleThreadFlagged_Remove(t *testing.T) {
	stub, cs, raw, eng := newSpyStub(t)
	ctx := context.Background()

	accID, _ := raw.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := raw.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 300})
	tid := threadID

	mkMsg := func(uid, date int64, flags string) int64 {
		id, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: flags,
		})
		require.NoError(t, err)
		return id
	}
	m1 := mkMsg(1, 100, `["\\Flagged"]`)
	mkMsg(2, 200, `[]`) // not flagged — must be skipped
	m3 := mkMsg(3, 300, `["\\Seen","\\Flagged"]`)

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	res, err := stub.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "removed", res.Action)
	require.Equal(t, int64(2), res.Count)
	require.Equal(t, int64(1), cs.toggleFlaggedCalls.Load())

	require.Len(t, eng.worker.ops, 1)
	op := eng.worker.ops[0]
	require.False(t, op.Add)
	require.Equal(t, []string{`\Flagged`}, op.Flags)
	require.ElementsMatch(t, []int64{1, 3}, op.UIDs,
		"both flagged UIDs in one bulk Op; the un-flagged UID 2 must be absent")

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
	require.Len(t, events, 2, "one MessageUpdated per flipped message")
	gotIDs := make([]int64, 0, len(events))
	for _, ev := range events {
		require.Equal(t, "MessageUpdated", ev.Type)
		id, ok := ev.Payload["id"].(int64)
		require.True(t, ok, "payload.id must be int64; got %T", ev.Payload["id"])
		gotIDs = append(gotIDs, id)
	}
	require.ElementsMatch(t, []int64{m1, m3}, gotIDs,
		"MessageUpdated must fire for the two flipped messages — m2 was unflagged, no event")
}

// TestToggleThreadFlagged_Noop — empty/unknown thread id. Storage IS
// consulted (it returns Action="noop") but no flag op, no SSE event.
func TestToggleThreadFlagged_Noop(t *testing.T) {
	stub, cs, _, eng := newSpyStub(t)
	ctx := context.Background()

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	res, err := stub.ToggleThreadFlagged(ctx, 999999)
	require.NoError(t, err)
	require.Equal(t, "noop", res.Action)
	require.Equal(t, int64(0), res.Count)
	require.Equal(t, int64(1), cs.toggleFlaggedCalls.Load(),
		"storage is still consulted even on noop")
	require.Empty(t, eng.worker.ops)
	select {
	case ev := <-evCh:
		t.Fatalf("no SSE event expected on noop, got %+v", ev)
	default:
	}
}

// TestToggleThreadFlagged_NoEngine — Stub.Engine == nil (unit-test wiring).
// Storage UPDATE still commits, SSE event still fires, no panic, no flag op.
func TestToggleThreadFlagged_NoEngine(t *testing.T) {
	dir := t.TempDir()
	s, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	cs := &countingStore{Writer: s}
	stub := NewStub(cs, sec, NewEmitter(), nil) // nil engine

	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := s.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 100})
	tid := threadID
	_, err = s.InsertMessage(ctx, storage.MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 100,
		ThreadID: &tid, Flags: `[]`,
	})
	require.NoError(t, err)

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	res, err := stub.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "added", res.Action)
	require.Equal(t, int64(1), res.Count)

	select {
	case ev := <-evCh:
		require.Equal(t, "MessageUpdated", ev.Type)
	default:
		t.Fatal("expected one MessageUpdated event even with nil engine")
	}
}

// TestToggleThreadFlagged_MultiFolder — synthetic edge case: a thread whose
// messages span two folders. Verifies the API layer groups by folder so
// each folder gets its own bulk Op (IMAP UIDs are folder-scoped — using
// one Op for both folders would address the wrong messages on the second
// folder).
func TestToggleThreadFlagged_MultiFolder(t *testing.T) {
	stub, _, raw, eng := newSpyStub(t)
	ctx := context.Background()

	accID, _ := raw.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	fA, _ := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	fB, _ := raw.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "Archive", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	threadID, _ := raw.InsertThread(ctx, storage.ThreadRow{SubjectNorm: "t", LastDate: 200})
	tid := threadID
	mkFlagged := func(folderID, uid, date int64) {
		_, err := raw.InsertMessage(ctx, storage.MessageRow{
			AccountID: accID, FolderID: folderID, UID: uid, Date: date,
			ThreadID: &tid, Flags: `["\\Flagged"]`,
		})
		require.NoError(t, err)
	}
	mkFlagged(fA, 11, 100)
	mkFlagged(fB, 22, 200)

	res, err := stub.ToggleThreadFlagged(ctx, threadID)
	require.NoError(t, err)
	require.Equal(t, "removed", res.Action)
	require.Equal(t, int64(2), res.Count)

	require.Len(t, eng.worker.ops, 2)
	got := map[int64][]int64{}
	for _, op := range eng.worker.ops {
		require.False(t, op.Add)
		require.Equal(t, []string{`\Flagged`}, op.Flags)
		got[op.FolderID] = op.UIDs
	}
	require.Equal(t, []int64{11}, got[fA])
	require.Equal(t, []int64{22}, got[fB])
}

// TestMarkFolderRead_NoEngine — Stub.Engine is nil (unit-test wiring path
// where sync isn't running). Storage UPDATE still commits, SSE event still
// fires, no panic on nil engine, no flag op queued.
func TestMarkFolderRead_NoEngine(t *testing.T) {
	dir := t.TempDir()
	s, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	key := make([]byte, 32)
	sec, err := secrets.Open(filepath.Join(dir, "secrets.bin"), key)
	require.NoError(t, err)

	cs := &countingStore{Writer: s}
	stub := NewStub(cs, sec, NewEmitter(), nil) // nil engine

	ctx := context.Background()
	accID, _ := s.InsertAccount(ctx, storage.AccountRow{
		Name: "a", Email: "a@b.c", IMAPHost: "h", IMAPPort: 993,
		IMAPUsername: "u", UseTLS: true, Color: "#fff", CreatedAt: 0,
	})
	folderID, _ := s.UpsertFolder(ctx, storage.FolderRow{
		AccountID: accID, Name: "INBOX", Delimiter: "/", UIDValidity: 1, UIDNext: 1,
	})
	_, err = s.InsertMessage(ctx, storage.MessageRow{
		AccountID: accID, FolderID: folderID, UID: 1, Date: 1, Flags: `[]`,
	})
	require.NoError(t, err)

	evCh, unsub := stub.Emitter.Subscribe()
	defer unsub()

	count, err := stub.MarkFolderRead(ctx, folderID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	select {
	case ev := <-evCh:
		require.Equal(t, "FolderMarkedRead", ev.Type)
	default:
		t.Fatal("expected one FolderMarkedRead event even with nil engine")
	}
}
