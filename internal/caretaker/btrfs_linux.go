package caretaker

import "syscall"

const btrfsMagic = 0x9123683E

// isBtrfs says whether path is on btrfs, so it can be snapshotted.
func isBtrfs(path string) bool {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return false
	}
	return uint64(st.Type) == btrfsMagic
}
