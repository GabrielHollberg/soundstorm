//go:build windows

package main

import "os"

// Windows has no uid or gid to put back, and the case this guards against -
// a root process rewriting a file an unprivileged service has to read - is a
// container concern. The binary does build and run natively on Windows, so
// this exists to keep that true.
func restoreOwner(string, os.FileInfo) error { return nil }
