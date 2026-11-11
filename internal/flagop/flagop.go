// Package flagop holds the wire-side type for flag-mutation requests that
// flow from the API stub down to the per-account sync worker. It exists to
// dissolve the previous triple-decl (api.FlagOp ⇆ sync.FlagOp via a
// workerAdapter bridge in main.go), where adding a field on one side
// without touching the others would silently drop the field at the bridge.
package flagop

// Op describes an accepted local flag change for the worker wake-up adapter.
// Storage commits durable per-message intents with the local edit; workers
// drain those current, UIDVALIDITY-bound intents instead of replaying this
// transient payload. UIDs are scoped to the account's folder.
type Op struct {
	AccountID int64
	FolderID  int64
	UIDs      []int64
	Add       bool
	Flags     []string
}
