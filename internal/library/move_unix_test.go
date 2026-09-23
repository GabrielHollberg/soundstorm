//go:build unix

package library

import "syscall"

var errorNotSameDeviceForTest = syscall.EXDEV
