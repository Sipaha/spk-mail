package storage

import (
	"context"
	"database/sql"
)

// Reader is the read-only surface of Store. Methods on this interface execute
// against the read-pool connection and never block on a writer transaction
// (SQLite WAL: many readers + one writer).
type Reader interface {
	GetMessage(ctx context.Context, id int64) (MessageRow, error)
	GetMessagesByThread(ctx context.Context, threadID int64) ([]MessageRow, error)
	FindThreadByMessageIDs(ctx context.Context, msgIDs []string) (int64, bool, error)
	FindThreadBySubject(ctx context.Context, accountID int64, normSubject string, dateUnix, windowSecs int64) (int64, bool, error)

	ListAttachmentsByMessages(ctx context.Context, msgIDs []int64) (map[int64][]AttachmentRow, error)
	ListPendingAttachments(ctx context.Context, accountID int64, limit int) ([]PendingAttachment, error)
	GetAttachmentLocalPath(ctx context.Context, id int64) (string, bool, error)
	GetAttachmentBlob(ctx context.Context, id int64) (blobID int64, sha256 string, found bool, err error)

	ListFolders(ctx context.Context, accountID int64) ([]FolderRow, error)
	MaxUIDByFolder(ctx context.Context, folderID int64) (int64, error)

	ListThreads(ctx context.Context, f ThreadFilter, limit, offset int) ([]ThreadRow, error)
	ListThreadsRecent(ctx context.Context, limit, offset int) ([]ThreadRow, error)
	MessageCountsByFolder(ctx context.Context, accountID int64) (map[int64]FolderCounts, error)

	GetAccount(ctx context.Context, id int64) (AccountRow, error)
	ListAccounts(ctx context.Context) ([]AccountRow, error)
	AccountIsMuted(ctx context.Context, id int64) (bool, error)
	TotalUnreadExcludingMuted(ctx context.Context) (int64, error)

	GetProfile(ctx context.Context, id int64) (ProfileRow, error)
	ListProfiles(ctx context.Context) ([]ProfileRow, error)

	Search(ctx context.Context, query string, limit, offset int) ([]SearchHit, error)

	ListApprovedRemoteURLs(ctx context.Context) (map[string]bool, error)

	GetBlob(ctx context.Context, id int64) (BlobRow, error)
	ListZeroRefBlobs(ctx context.Context) ([]BlobRow, error)

	GetMessageRawBlob(ctx context.Context, msgID int64) (blobID int64, sha256 string, found bool, err error)
}

// Writer adds mutating methods on top of Reader. A Writer can also read —
// the embedded Reader methods physically run on the read pool regardless
// (the embedding documents the type contract; routing is fixed inside Store).
type Writer interface {
	Reader

	InsertMessage(ctx context.Context, m MessageRow) (int64, error)
	UpdateFlags(ctx context.Context, id int64, flagsJSON string) error
	UpdateFlagsByUID(ctx context.Context, folderID, uid int64, flagsJSON string) (msgID int64, threadID *int64, changed bool, err error)
	UpdateBodyHTML(ctx context.Context, id int64, html string) error
	MarkMessagesRead(ctx context.Context, ids []int64) (MarkReadOutcome, error)
	MarkFolderMessagesRead(ctx context.Context, folderID int64) (MarkReadOutcome, error)
	ToggleThreadFlagged(ctx context.Context, threadID int64) (FlagToggleOutcome, error)

	InsertAttachment(ctx context.Context, a AttachmentRow) (int64, error)
	UpdateAttachmentDownloaded(ctx context.Context, id int64, blobID int64, sha256 string, ts int64) error
	ClearAttachmentBlob(ctx context.Context, id int64) (*int64, error)

	UpsertFolder(ctx context.Context, r FolderRow) (int64, error)
	SetFolderHighestModSeq(ctx context.Context, folderID int64, modseq uint64) error
	DeleteMessagesByFolder(ctx context.Context, folderID int64) error

	UpdateThreadStats(ctx context.Context, threadID int64) error

	InsertAccount(ctx context.Context, a AccountRow) (int64, error)
	DeleteAccount(ctx context.Context, id int64) error

	InsertProfile(ctx context.Context, p ProfileRow) (int64, error)
	UpdateProfile(ctx context.Context, id int64, name, color string) error
	DeleteProfile(ctx context.Context, id int64) error
	SetProfileMuted(ctx context.Context, id int64, muted bool) error

	InsertParsedMessageBundle(ctx context.Context, b MessageBundle) (int64, int64, error)
	WithTx(ctx context.Context, fn func(*sql.Tx) error) error

	AddApprovedRemoteURLs(ctx context.Context, urls []string) error

	InsertOrIncBlob(ctx context.Context, sha256 string, size, nowUnix int64) (int64, bool, error)
	DecBlobRef(ctx context.Context, blobID int64) (int64, error)
	DeleteBlobIfZero(ctx context.Context, blobID int64) (bool, error)
	SweepBlobs(ctx context.Context, dataDir string) (deletedRows, deletedBytes int64, err error)
	BackfillLegacyAttachments(ctx context.Context, dataDir string) (migrated int, err error)

	SetMessageRawBlob(ctx context.Context, msgID, blobID, capturedAtUnix int64) (SetRawResult, int64, error)
	ClearMessageRawBlob(ctx context.Context, msgID int64) (*int64, error)
}

// Compile-time assertion: *Store must implement both interfaces. If you add a
// method to Reader/Writer above, this line will refuse to compile until Store
// has the method.
var (
	_ Reader = (*Store)(nil)
	_ Writer = (*Store)(nil)
)

// MarkReadOutcome reports the side-effects of the mark-read storage methods
// (MarkMessagesRead and MarkFolderMessagesRead) so the API layer can emit
// MessageUpdated / FolderMarkedRead events and submit IMAP STORE flag ops
// without re-reading the messages.
type MarkReadOutcome struct {
	// Changed lists messages that flipped from !\Seen → \Seen. Already-seen
	// IDs in the input are absent.
	Changed []MarkReadChange
	// ChangedThreadIDs is the deduped set of thread_ids whose stats were
	// recomputed (only threads of messages in Changed are touched). Order
	// is unspecified — populated from a map iteration.
	ChangedThreadIDs []int64
}

// MarkReadChange carries the per-message metadata the API layer needs after
// a successful flag flip: the IMAP folder/UID coordinates for STORE, and the
// thread_id (nullable in the schema — nil means the message was never threaded).
type MarkReadChange struct {
	MessageID int64
	AccountID int64
	FolderID  int64
	UID       int64
	ThreadID  *int64
}

// FlagToggleOutcome is the return shape of ToggleThreadFlagged. Mirrors
// MarkReadOutcome in spirit (per-message metadata for the API layer to
// fan IMAP STORE ops out as bulk flagop.Op) but adds an Action so the
// API layer knows whether to emit add or remove on the IMAP side.
//
// Action values:
//   - "added"   — flag was added to the most-recent message in the thread
//   - "removed" — flag was removed from every message in the thread that
//                 had it
//   - "noop"    — thread was empty / unknown id; nothing to do
type FlagToggleOutcome struct {
	Action  string
	Changed []FlagChange
}

// FlagChange carries the IMAP coordinates the API layer needs to fan an
// AccountWorker STORE op out without re-reading message rows.
type FlagChange struct {
	MessageID int64
	AccountID int64
	FolderID  int64
	UID       int64
}
