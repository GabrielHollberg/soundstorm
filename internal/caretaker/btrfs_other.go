//go:build !linux

package caretaker

// isBtrfs is false off Linux: the caretaker only runs on the box.
func isBtrfs(string) bool { return false }
