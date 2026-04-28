package sync

import (
	"time"

	"github.com/spk/spk-mail/internal/imap"
)

// IncomingMessage is what AccountWorkers send to the StoreWriter.
type IncomingMessage struct {
	AccountID  int64
	FolderID   int64
	FolderRole string // "inbox"|"sent"|... — used to decide notification emission
	UID        int64
	Flags      []string
	InternalAt time.Time
	Raw        []byte // RFC822
	IsResync   bool   // suppress notifications

	// Ack is an optional callback invoked by the writer after this
	// message has been fully processed (successfully or with error). It
	// lets a producer wait for batch drain before emitting follow-on
	// state (e.g. AccountWorker.syncFolder uses it to delay SyncProgress
	// until the matching MessageInserted events have actually fired, so
	// the frontend doesn't show "all synced" while rows are still
	// being committed).
	Ack func()
}

// AccountStatus is emitted as an event whenever an account's state changes.
type AccountStatus struct {
	AccountID int64
	State     string // connecting|ok|error|offline
	Detail    string
}

var _ = imap.DialOpts{} // keep import
