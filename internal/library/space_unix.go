//go:build unix

package library

import "syscall"

// freeSpace reports the bytes available to an unprivileged writer under dir.
//
// Available rather than free: the two differ by the reserve a filesystem keeps
// for root, and SoundStorm is not root.
func freeSpace(dir string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true
}
