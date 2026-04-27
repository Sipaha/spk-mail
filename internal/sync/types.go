package sync

import (
	"time"

	"github.com/spk/spk-mail/internal/imap"
)

// IncomingMessage is what AccountWorkers send to the StoreWriter.
type IncomingMessage struct {
	AccountID  int64
	FolderID   int64
	UID        int64
	Flags      []string
	InternalAt time.Time
	Raw        []byte // RFC822
	IsResync   bool   // suppress notifications
}

// FlagOp is a request from the API layer to change flags on a stored message.
// The corresponding AccountWorker translates it to a UID STORE.
type FlagOp struct {
	AccountID int64
	FolderUID FolderUID // (folder_id, uid)
	Add       bool
	Flags     []string
}

type FolderUID struct {
	FolderID int64
	UID      int64
}

// AccountStatus is emitted as an event whenever an account's state changes.
type AccountStatus struct {
	AccountID int64
	State     string // connecting|ok|error|offline
	Detail    string
}

var _ = imap.DialOpts{} // keep import
