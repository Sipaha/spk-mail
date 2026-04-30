//go:build windows

package fsutil

// isCrossDevice always returns false on Windows: MoveFileEx already
// handles cross-volume moves transparently when the
// MOVEFILE_COPY_ALLOWED flag is in effect, which it is by default
// for os.Rename in the Go runtime.
func isCrossDevice(_ error) bool { return false }
