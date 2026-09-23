//go:build !unix

package library

import (
	"errors"
	"syscall"
)

// errorNotSameDevice is Windows' ERROR_NOT_SAME_DEVICE, its answer to a move
// between drives.
const errorNotSameDevice = syscall.Errno(17)

// crossDevice reports whether a rename failed because source and destination
// are on different drives.
func crossDevice(err error) bool { return errors.Is(err, errorNotSameDevice) }
