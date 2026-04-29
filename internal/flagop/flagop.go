// Package flagop holds the wire-side type for flag-mutation requests that
// flow from the API stub down to the per-account sync worker. It exists to
// dissolve the previous triple-decl (api.FlagOp ⇆ sync.FlagOp via a
// workerAdapter bridge in main.go), where adding a field on one side
// without touching the others would silently drop the field at the bridge.
package flagop

// Op is a request to add or remove flags on a set of IMAP messages within
// one folder. The AccountWorker for AccountID receives it via its
// SubmitFlagOp method and translates it to a single UID STORE on the named
// folder. UIDs of length 1 is the per-message case (e.g. mark one read);
// larger slices come from bulk operations like "mark all in folder read".
type Op struct {
	AccountID int64
	FolderID  int64
	UIDs      []int64
	Add       bool
	Flags     []string
}
