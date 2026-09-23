//go:build unix

package library

import (
	"errors"
	"syscall"
)

// crossDevice reports whether a rename failed because source and destination
// are on different filesystems.
func crossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }
