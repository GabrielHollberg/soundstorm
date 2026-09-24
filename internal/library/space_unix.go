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

// diskSize reports the bytes available to an unprivileged writer and the size
// of the filesystem under dir. Checked on Docker Desktop for Windows, where a
// bind-mounted library is a Windows drive: df inside a container reported the
// same 826 GB free of 931 GB that Windows did, so this is the drive the files
// are on, not Docker's own disk.
func diskSize(dir string) (free, total uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), true
}
