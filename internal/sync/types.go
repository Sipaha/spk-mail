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
}

// AccountStatus is emitted as an event whenever an account's state changes.
type AccountStatus struct {
	AccountID int64
	State     string // connecting|ok|error|offline
	Detail    string
}

var _ = imap.DialOpts{} // keep import
