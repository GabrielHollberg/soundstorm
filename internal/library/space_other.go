//go:build !unix

package library

// freeSpace has no portable answer off Unix, and the alternative is a
// kernel32 call through syscall.NewLazyDLL for the one platform SoundStorm is
// not deployed on. Everything that consumes this treats "unknown" as a reason
// to say less rather than to guess, so the cost is a vaguer error message on a
// native Windows build.
func freeSpace(string) (uint64, bool) { return 0, false }

func diskSize(string) (uint64, uint64, bool) { return 0, 0, false }
