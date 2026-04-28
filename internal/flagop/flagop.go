// Package flagop holds the wire-side type for flag-mutation requests that
// flow from the API stub down to the per-account sync worker. It exists to
// dissolve the previous triple-decl (api.FlagOp ⇆ sync.FlagOp via a
// workerAdapter bridge in main.go), where adding a field on one side
// without touching the others would silently drop the field at the bridge.
package flagop

// FolderUID identifies a single message on the IMAP server within an
// account's folder set. The IMAP UID is unique per (folder, uidvalidity);
// pairing it with the FolderID lets the sync worker translate to a
// SELECT + UID STORE without a back-trip through storage.
type FolderUID struct {
	FolderID int64
	UID      int64
}

// Op is a request to add or remove flags on one IMAP message. The
// AccountWorker for AccountID receives it via its SubmitFlagOp method
// and translates it to a UID STORE on the right folder.
type Op struct {
	AccountID int64
	FolderUID FolderUID
	Add       bool
	Flags     []string
}
