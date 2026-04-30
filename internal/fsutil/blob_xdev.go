//go:build !windows

package fsutil

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether err is the POSIX EXDEV "invalid
// cross-device link" code that os.Rename surfaces when source and
// destination are on different filesystems. Used to trigger the
// copy-then-rename fallback in WriteContentAddressed.
func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
